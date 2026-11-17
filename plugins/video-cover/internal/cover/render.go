package cover

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	TemplateID        = "wanmei-d-v1"
	NeutralTemplateID = "neutral-title-v1"
	BaseWidth         = 1728
	BaseHeight        = 2304
)

type Assets struct {
	SerifFont []byte
	SansFont  []byte
	SealPNG   []byte
}

type OutputSpec struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Filename string `json:"filename,omitempty"`
	Format   string `json:"format,omitempty"`
}

type RenderInput struct {
	WorkspaceRoot string       `json:"workspace_root"`
	SourceImage   string       `json:"source_image"`
	OutputDir     string       `json:"output_dir"`
	Filename      string       `json:"filename"`
	TemplateID    string       `json:"template_id"`
	Title         string       `json:"title"`
	Detail        string       `json:"detail"`
	FocusX        float64      `json:"focus_x"`
	FocusY        float64      `json:"focus_y"`
	Outputs       []OutputSpec `json:"outputs"`
	Inline        bool         `json:"inline"`
}

type RenderedFile struct {
	Path      string `json:"path"`
	Relative  string `json:"relative_path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Format    string `json:"format"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type Quality struct {
	TitleZoneMeanLuma float64 `json:"title_zone_mean_luma"`
	TitleZoneStdDev   float64 `json:"title_zone_stddev"`
	TitleSafeScore    int     `json:"title_safe_score"`
	TitleLines        int     `json:"title_lines"`
	DetailLines       int     `json:"detail_lines"`
}

type RenderResult struct {
	TemplateID     string         `json:"template_id"`
	Files          []RenderedFile `json:"files"`
	PreviewDataURL string         `json:"preview_data_url,omitempty"`
	Quality        Quality        `json:"quality"`
	Warnings       []string       `json:"warnings"`
}

type templateSpec struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	BaseWidth   int    `json:"base_width"`
	BaseHeight  int    `json:"base_height"`
	SignatureCN string `json:"signature_cn"`
	SignatureEN string `json:"signature_en"`
}

var (
	fontOnce  sync.Once
	serifFont *opentype.Font
	sansFont  *opentype.Font
	fontErr   error
)

func Templates() []templateSpec {
	return []templateSpec{{
		ID: TemplateID, Name: "皖美拾光 · 中英联署", Version: "1.0.0",
		BaseWidth: BaseWidth, BaseHeight: BaseHeight,
		SignatureCN: "皖美拾光", SignatureEN: "LIGHT, GATHERED IN ANHUI",
	}, {
		ID: NeutralTemplateID, Name: "简洁标题 · 无品牌", Version: "1.0.0",
		BaseWidth: 1080, BaseHeight: 1440,
	}}
}

func Render(assets Assets, in RenderInput) (RenderResult, error) {
	templateID := firstNonEmpty(strings.TrimSpace(in.TemplateID), TemplateID)
	if templateID != TemplateID && templateID != NeutralTemplateID {
		return RenderResult{}, errors.New("封面模板不可用，请重新选择")
	}
	if strings.TrimSpace(in.WorkspaceRoot) == "" {
		return RenderResult{}, errors.New("workspace_root 不能为空")
	}
	root, err := filepath.Abs(in.WorkspaceRoot)
	if err != nil {
		return RenderResult{}, err
	}
	source, err := resolveExisting(root, in.SourceImage)
	if err != nil {
		return RenderResult{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return RenderResult{}, errors.New("标题不能为空")
	}

	fontOnce.Do(func() {
		serifFont, fontErr = opentype.Parse(assets.SerifFont)
		if fontErr != nil {
			return
		}
		sansFont, fontErr = opentype.Parse(assets.SansFont)
	})
	if fontErr != nil {
		return RenderResult{}, fmt.Errorf("字体加载失败：%w", fontErr)
	}

	imgFile, err := os.Open(source)
	if err != nil {
		return RenderResult{}, err
	}
	src, _, err := image.Decode(imgFile)
	_ = imgFile.Close()
	if err != nil {
		return RenderResult{}, fmt.Errorf("读取底图失败：%w", err)
	}

	seal, err := png.Decode(bytes.NewReader(assets.SealPNG))
	if err != nil {
		return RenderResult{}, fmt.Errorf("印章资源损坏：%w", err)
	}
	outputs := normalizedOutputs(in.Outputs)
	outDir := in.OutputDir
	if strings.TrimSpace(outDir) == "" {
		outDir = filepath.Join(".himind", "video-cover")
	}
	absOut, err := resolveOutputDir(root, outDir)
	if err != nil {
		return RenderResult{}, err
	}
	if err := os.MkdirAll(absOut, 0o755); err != nil {
		return RenderResult{}, err
	}

	result := RenderResult{TemplateID: templateID, Warnings: []string{}}
	var previewBytes []byte
	previewFormat := "png"
	for idx, spec := range outputs {
		if spec.Width <= 0 || spec.Height <= 0 {
			return RenderResult{}, errors.New("输出尺寸必须大于 0")
		}
		if spec.Width > 4096 || spec.Height > 4096 {
			return RenderResult{}, errors.New("封面最长边不能超过 4096 像素")
		}
		if templateID == TemplateID && !sameAspect(spec.Width, spec.Height) {
			return RenderResult{}, fmt.Errorf("输出尺寸必须是 3:4：%dx%d", spec.Width, spec.Height)
		}
		var canvas *image.RGBA
		var quality Quality
		var warnings []string
		if templateID == NeutralTemplateID {
			canvas, quality, warnings, err = renderNeutral(src, sansFont, spec.Width, spec.Height, in.Title, in.Detail, in.FocusX, in.FocusY)
		} else {
			canvas, quality, warnings, err = renderOne(src, seal, serifFont, sansFont, spec.Width, spec.Height, in.Title, in.Detail, in.FocusX, in.FocusY)
		}
		if err != nil {
			return RenderResult{}, err
		}
		if idx == 0 {
			result.Quality = quality
		}
		result.Warnings = appendUnique(result.Warnings, warnings...)
		format := normalizeFormat(spec.Format)
		filename := strings.TrimSpace(spec.Filename)
		if filename == "" {
			base := sanitizeFilename(firstNonEmpty(in.Filename, "wanmei-cover"))
			filename = fmt.Sprintf("%s-%dx%d.%s", base, spec.Width, spec.Height, format)
		}
		if !strings.HasSuffix(strings.ToLower(filename), "."+format) {
			filename += "." + format
		}
		outputPath := filepath.Join(absOut, filename)
		if !within(root, outputPath) {
			return RenderResult{}, errors.New("输出路径越界")
		}
		data, err := encodeImage(canvas, format)
		if err != nil {
			return RenderResult{}, err
		}
		if err := atomicWrite(outputPath, data, 0o644); err != nil {
			return RenderResult{}, err
		}
		sum := sha256.Sum256(data)
		rel, _ := filepath.Rel(root, outputPath)
		result.Files = append(result.Files, RenderedFile{Path: outputPath, Relative: rel, Width: spec.Width, Height: spec.Height, Format: format, SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))})
		if idx == 0 {
			previewBytes, previewFormat = data, format
		}
	}
	if in.Inline && len(previewBytes) > 0 {
		result.PreviewDataURL = "data:image/" + previewFormat + ";base64," + base64.StdEncoding.EncodeToString(previewBytes)
	}
	return result, nil
}

// A neutral layout deliberately carries no signature, seal or platform branding.
func renderNeutral(src image.Image, textFont *opentype.Font, width, height int, title, detail string, focusX, focusY float64) (*image.RGBA, Quality, []string, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(canvas, canvas.Bounds(), src, cropRect(src.Bounds(), width, height, focusX, focusY), xdraw.Src, nil)
	for y := height / 2; y < height; y++ {
		alpha := float64(y-height/2) / float64(height-height/2) * 0.78
		for x := 0; x < width; x++ {
			c := canvas.RGBAAt(x, y)
			canvas.SetRGBA(x, y, color.RGBA{uint8(float64(c.R) * (1 - alpha)), uint8(float64(c.G) * (1 - alpha)), uint8(float64(c.B) * (1 - alpha)), 255})
		}
	}
	fontSize := float64(width) * 0.058
	if float64(height)*0.09 < fontSize {
		fontSize = float64(height) * 0.09
	}
	face, metrics, err := newFace(textFont, fontSize)
	if err != nil {
		return nil, Quality{}, nil, err
	}
	defer face.Close()
	lines := wrapText(face, title, float64(width)*0.84)
	if len(lines) > 3 {
		return nil, Quality{}, nil, errors.New("主标题超过三行，请缩短后重试")
	}
	detailFace, detailMetrics, err := newFace(textFont, fontSize*0.55)
	if err != nil {
		return nil, Quality{}, nil, err
	}
	defer detailFace.Close()
	details := wrapText(detailFace, detail, float64(width)*0.84)
	if len(details) > 2 {
		return nil, Quality{}, nil, errors.New("补充信息超过两行，请缩短后重试")
	}
	top := float64(height)*0.9 - float64(len(lines))*fontSize*1.3 - float64(len(details))*fontSize*0.8
	for index, line := range lines {
		drawShadowedText(canvas, face, float64(width)*0.08, top+float64(index)*fontSize*1.3+float64(metrics.Ascent)/64, line, color.NRGBA{255, 255, 255, 255}, 0)
	}
	detailTop := top + float64(len(lines))*fontSize*1.3 + fontSize*0.2
	for index, line := range details {
		drawShadowedText(canvas, detailFace, float64(width)*0.08, detailTop+float64(index)*fontSize*0.75+float64(detailMetrics.Ascent)/64, line, color.NRGBA{240, 240, 240, 255}, 0)
	}
	quality := Quality{TitleLines: len(lines), DetailLines: len(details)}
	return canvas, quality, nil, nil
}

func renderOne(src image.Image, seal image.Image, serifFont, sansFont *opentype.Font, width, height int, title, detail string, focusX, focusY float64) (*image.RGBA, Quality, []string, error) {
	warnings := []string{}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	crop := cropRect(src.Bounds(), width, height, focusX, focusY)
	xdraw.CatmullRom.Scale(canvas, canvas.Bounds(), src, crop, xdraw.Src, nil)
	paintAtmosphere(canvas)
	quality := measureTitleZone(canvas, width, height)
	if quality.TitleZoneMeanLuma < 28 {
		warnings = append(warnings, "标题区画面过暗，可能是近黑帧，请换一帧")
	}
	if quality.TitleZoneMeanLuma > 210 {
		warnings = append(warnings, "标题区画面过亮，建议换一帧或提高暗部")
	}
	scale := float64(width) / BaseWidth
	drawFrame(canvas, scale)

	signatureY := 166.0 * scale
	drawVerticalRule(canvas, 104*scale, signatureY, 5*scale, 340*scale)
	lineY := signatureY + 17*scale
	fillRect(canvas, 145*scale, lineY, 38*scale, maxFloat(2*scale, 1), color.NRGBA{183, 53, 37, 255})
	sigX := 198.0 * scale
	sigFace, sigMetrics, err := newFace(sansFont, 37*scale)
	if err != nil {
		return nil, Quality{}, nil, err
	}
	defer sigFace.Close()
	drawShadowedText(canvas, sigFace, sigX, signatureY+float64(sigMetrics.Ascent)/64, "皖美拾光", color.NRGBA{223, 193, 122, 245}, 0)
	cnWidth := textWidth(sigFace, "皖美拾光")
	dotX := sigX + cnWidth + 15*scale
	drawShadowedText(canvas, sigFace, dotX, signatureY+float64(sigMetrics.Ascent)/64, "·", color.NRGBA{223, 193, 122, 160}, 0)
	enFace, enMetrics, err := newFace(sansFont, 22*scale)
	if err != nil {
		return nil, Quality{}, nil, err
	}
	defer enFace.Close()
	enX := dotX + textWidth(sigFace, "·") + 15*scale
	drawShadowedText(canvas, enFace, enX, signatureY+float64(sigMetrics.Ascent)/64, "LIGHT, GATHERED IN ANHUI", color.NRGBA{245, 240, 229, 220}, 0)
	_ = enMetrics

	titleX := 145.0 * scale
	titleTop := 242.0 * scale
	titleSize := 108.0 * scale
	maxTitleWidth := 1360.0 * scale
	titleFace, titleMetrics, err := newFace(serifFont, titleSize)
	if err != nil {
		return nil, Quality{}, nil, err
	}
	defer titleFace.Close()
	titleLines := wrapText(titleFace, title, maxTitleWidth)
	if len(titleLines) > 2 {
		titleSize = 92 * scale
		titleFace.Close()
		titleFace, titleMetrics, err = newFace(serifFont, titleSize)
		if err != nil {
			return nil, Quality{}, nil, err
		}
		titleLines = wrapText(titleFace, title, maxTitleWidth)
	}
	if len(titleLines) > 2 {
		return nil, Quality{}, nil, errors.New("主标题超过两行，请缩短或手动断行")
	}
	quality.TitleLines = len(titleLines)
	lineHeight := titleSize * 1.12
	for i, line := range titleLines {
		baseline := titleTop + float64(i)*lineHeight + float64(titleMetrics.Ascent)/64
		drawShadowedText(canvas, titleFace, titleX, baseline, line, color.NRGBA{252, 249, 241, 255}, maxFloat(1.15*scale, 0.7))
	}
	detailTop := titleTop + float64(len(titleLines))*lineHeight + 22*scale
	detailSize := 68 * scale
	detailFace, detailMetrics, err := newFace(serifFont, detailSize)
	if err != nil {
		return nil, Quality{}, nil, err
	}
	defer detailFace.Close()
	detailLines := wrapText(detailFace, detail, maxTitleWidth)
	if len(detailLines) > 2 {
		return nil, Quality{}, nil, errors.New("补充信息超过两行，请缩短")
	}
	quality.DetailLines = len(detailLines)
	detailHeight := detailSize * 1.34
	for i, line := range detailLines {
		baseline := detailTop + float64(i)*detailHeight + float64(detailMetrics.Ascent)/64
		drawShadowedText(canvas, detailFace, titleX, baseline, line, color.NRGBA{241, 213, 143, 255}, maxFloat(0.8*scale, 0.45))
	}
	sealSize := int(202*scale + 0.5)
	sealX := width - int(88*scale) - sealSize
	sealY := height - int(70*scale) - sealSize
	xdraw.CatmullRom.Scale(canvas, image.Rect(sealX, sealY, sealX+sealSize, sealY+sealSize), seal, seal.Bounds(), xdraw.Over, nil)
	paintGrain(canvas)
	return canvas, quality, warnings, nil
}

func newFace(f *opentype.Font, size float64) (font.Face, font.Metrics, error) {
	if size < 1 {
		size = 1
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, font.Metrics{}, err
	}
	return face, face.Metrics(), nil
}

func textWidth(face font.Face, text string) float64 {
	return float64(font.MeasureString(face, text)) / 64
}

func wrapText(face font.Face, text string, maxWidth float64) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	paras := strings.Split(text, "\n")
	out := []string{}
	for _, para := range paras {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		var line strings.Builder
		for _, r := range para {
			next := line.String() + string(r)
			if line.Len() > 0 && textWidth(face, next) > maxWidth {
				out = append(out, strings.TrimSpace(line.String()))
				line.Reset()
			}
			line.WriteRune(r)
		}
		if line.Len() > 0 {
			out = append(out, strings.TrimSpace(line.String()))
		}
	}
	return out
}

func drawShadowedText(dst *image.RGBA, face font.Face, x, baseline float64, text string, col color.NRGBA, bold float64) {
	for _, off := range []struct{ x, y float64 }{{0, 1}, {0, 4}} {
		alpha := uint8(90)
		if off.y > 2 {
			alpha = 170
		}
		drawText(dst, face, x+off.x, baseline+off.y, text, color.NRGBA{0, 0, 0, alpha}, 0)
	}
	drawText(dst, face, x, baseline, text, col, bold)
}

func drawText(dst *image.RGBA, face font.Face, x, baseline float64, text string, col color.NRGBA, bold float64) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: face, Dot: fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)}}
	d.DrawString(text)
	if bold > 0 {
		for _, off := range []struct{ x, y float64 }{{bold, 0}, {0, bold}, {bold, bold}} {
			d.Dot = fixed.Point26_6{X: fixed.Int26_6((x + off.x) * 64), Y: fixed.Int26_6((baseline + off.y) * 64)}
			d.DrawString(text)
		}
	}
}

func cropRect(bounds image.Rectangle, dstW, dstH int, focusX, focusY float64) image.Rectangle {
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return bounds
	}
	target := float64(dstW) / float64(dstH)
	current := float64(w) / float64(h)
	cw, ch := w, h
	if current > target {
		cw = int(float64(h)*target + 0.5)
	} else if current < target {
		ch = int(float64(w)/target + 0.5)
	}
	if focusX < 0 {
		focusX = 0
	}
	if focusX > 1 {
		focusX = 1
	}
	if focusY < 0 {
		focusY = 0
	}
	if focusY > 1 {
		focusY = 1
	}
	x := bounds.Min.X + int((float64(w-cw))*focusX+0.5)
	y := bounds.Min.Y + int((float64(h-ch))*focusY+0.5)
	return image.Rect(x, y, x+cw, y+ch)
}

func paintAtmosphere(dst *image.RGBA) {
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		ty := float64(y) / float64(h-1)
		va := verticalAlpha(ty)
		for x := 0; x < w; x++ {
			nx := (float64(x)/float64(w-1) - .5) / .5
			ny := (ty - .43) / .57
			vig := 0.21 * smooth(clamp01((nx*nx+ny*ny-0.25)/.75))
			sx := (float64(x)/float64(w) - .10) / .62
			sy := (ty - .17) / .24
			local := 0.20 * clamp01(1-(sx*sx+sy*sy))
			a := clamp01(va + vig + local)
			blendPixel(dst, x, y, color.NRGBA{5, 10, 11, uint8(a*255 + 0.5)})
		}
	}
}
func verticalAlpha(t float64) float64 {
	switch {
	case t < .15:
		return .78 - (t/.15)*.18
	case t < .34:
		return .60 - ((t-.15)/.19)*.43
	case t < .48:
		return .17 - ((t-.34)/.14)*.17
	case t < .69:
		return 0
	case t < .85:
		return ((t - .69) / .16) * .43
	default:
		return .43 + ((t-.85)/.15)*.48
	}
}
func paintGrain(dst *image.RGBA) {
	b := dst.Bounds()
	seed := uint32(0x9e3779b9)
	for y := b.Min.Y; y < b.Max.Y; y += 3 {
		for x := b.Min.X; x < b.Max.X; x += 3 {
			seed = seed*1664525 + 1013904223
			if seed&7 != 0 {
				continue
			}
			c := uint8(80 + seed%96)
			blendPixel(dst, x, y, color.NRGBA{c, c, c, 7})
		}
	}
}
func blendPixel(dst *image.RGBA, x, y int, c color.NRGBA) {
	if !image.Pt(x, y).In(dst.Bounds()) {
		return
	}
	sa := uint32(c.A)
	da := 255 - sa
	r := (uint32(c.R)*sa + uint32(dst.Pix[dst.PixOffset(x, y)])*da) / 255
	g := (uint32(c.G)*sa + uint32(dst.Pix[dst.PixOffset(x, y)+1])*da) / 255
	b := (uint32(c.B)*sa + uint32(dst.Pix[dst.PixOffset(x, y)+2])*da) / 255
	o := dst.PixOffset(x, y)
	dst.Pix[o] = uint8(r)
	dst.Pix[o+1] = uint8(g)
	dst.Pix[o+2] = uint8(b)
	if sa == 255 {
		dst.Pix[o+3] = 255
	} else {
		na := sa + uint32(dst.Pix[o+3])*da/255
		dst.Pix[o+3] = uint8(na)
	}
}
func drawFrame(dst *image.RGBA, scale float64) {
	b := dst.Bounds()
	inset := int(28*scale + 0.5)
	th := maxInt(int(2.5*scale+0.5), 1)
	c := color.NRGBA{245, 240, 229, 74}
	fillRect(dst, float64(inset), float64(inset), float64(b.Dx()-2*inset), float64(th), c)
	fillRect(dst, float64(inset), float64(b.Dy()-inset-th), float64(b.Dx()-2*inset), float64(th), c)
	fillRect(dst, float64(inset), float64(inset+th), float64(th), float64(b.Dy()-2*inset-2*th), c)
	fillRect(dst, float64(b.Dx()-inset-th), float64(inset+th), float64(th), float64(b.Dy()-2*inset-2*th), c)
}
func drawVerticalRule(dst *image.RGBA, x, y, w, h float64) {
	steps := int(h)
	if steps < 1 {
		return
	}
	for i := 0; i < steps; i++ {
		t := float64(i) / float64(maxInt(steps-1, 1))
		a := uint8(200*(1-t) + 50*t)
		fillRect(dst, x, y+float64(i), w, 1, color.NRGBA{183, 53, 37, a})
	}
}
func fillRect(dst *image.RGBA, x, y, w, h float64, c color.NRGBA) {
	x0 := int(x + 0.5)
	y0 := int(y + 0.5)
	x1 := int(x + w + 0.5)
	y1 := int(y + h + 0.5)
	for yy := y0; yy < y1; yy++ {
		for xx := x0; xx < x1; xx++ {
			blendPixel(dst, xx, yy, c)
		}
	}
}
func measureTitleZone(img *image.RGBA, w, h int) Quality {
	x0, y0 := int(float64(w)*.06), int(float64(h)*.06)
	x1, y1 := int(float64(w)*.63), int(float64(h)*.28)
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	sum, sum2, n := 0.0, 0.0, 0.0
	for y := y0; y < y1; y += 2 {
		for x := x0; x < x1; x += 2 {
			o := img.PixOffset(x, y)
			l := .2126*float64(img.Pix[o]) + .7152*float64(img.Pix[o+1]) + .0722*float64(img.Pix[o+2])
			sum += l
			sum2 += l * l
			n++
		}
	}
	mean := 0.0
	std := 0.0
	if n > 0 {
		mean = sum / n
		std = mathSqrt(maxFloat(sum2/n-mean*mean, 0))
	}
	penalty := absFloat(mean-105)*.15 + std*.75
	if mean < 30 {
		penalty += (30 - mean) * 1.8
	}
	if mean > 210 {
		penalty += (mean - 210) * 1.2
	}
	score := int(100 - penalty)
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return Quality{TitleZoneMeanLuma: mean, TitleZoneStdDev: std, TitleSafeScore: score}
}
func mathSqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 12; i++ {
		x = .5 * (x + v/x)
	}
	return x
}
func sameAspect(w, h int) bool { return absFloat(float64(w)/float64(h)-.75) < .001 }
func normalizedOutputs(in []OutputSpec) []OutputSpec {
	if len(in) == 0 {
		return []OutputSpec{{Width: 1080, Height: 1440, Format: "png"}, {Width: 1728, Height: 2304, Format: "png"}}
	}
	return in
}
func normalizeFormat(f string) string {
	f = strings.ToLower(strings.TrimSpace(f))
	if f == "jpg" || f == "jpeg" {
		return "jpeg"
	}
	if f == "png" {
		return "png"
	}
	return "png"
}
func encodeImage(img image.Image, format string) ([]byte, error) {
	var b bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&b, img, &jpeg.Options{Quality: 94})
	} else {
		err = png.Encode(&b, img)
	}
	return b.Bytes(), err
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func resolveExisting(root, path string) (string, error) {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", err
	}
	if !within(root, p) {
		return "", errors.New("路径越界")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", errors.New("需要图片文件")
	}
	return p, nil
}
func resolveOutputDir(root, path string) (string, error) {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", err
	}
	if !within(root, p) {
		return "", errors.New("输出目录越界")
	}
	return p, nil
}
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func sanitizeFilename(s string) string {
	repl := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", "\"", "-", "<", "-", ">", "-", "|", "-")
	s = strings.TrimSpace(repl.Replace(s))
	if s == "" {
		s = "wanmei-cover"
	}
	r := []rune(s)
	if len(r) > 80 {
		r = r[:80]
	}
	return string(r)
}
func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
func appendUnique(dst []string, items ...string) []string {
	seen := map[string]bool{}
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range items {
		if v != "" && !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return dst
}
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func smooth(v float64) float64 { v = clamp01(v); return v * v * (3 - 2*v) }
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func runeCount(s string) int { return utf8.RuneCountInString(s) }

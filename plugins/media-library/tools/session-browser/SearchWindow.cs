using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Security.AccessControl;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Microsoft.Web.WebView2.Core;
using Microsoft.Web.WebView2.WinForms;

// Ordinary browsing only: navigate the official search page, read rendered
// work links, and scroll. No private API signing, interception or challenge solving.
internal sealed partial class SearchWindow : Form
{
    private readonly JavaScriptSerializer json = new JavaScriptSerializer { MaxJsonLength = 2097152 };
    private readonly string root, requestDir, profile, requestID;
    private readonly SearchRequest request;
    private readonly WebView2 browser = new WebView2 { Dock = DockStyle.Fill };
    private readonly Label status = new Label { Dock = DockStyle.Top, Height = 54, Padding = new Padding(16, 10, 16, 4), Text = "正在搜索官网作品；如出现验证，请在此窗口完成。" };
    private readonly Timer timer = new Timer { Interval = 1000 };
    private readonly DateTime started = DateTime.UtcNow;
    private readonly List<SearchCard> cards = new List<SearchCard>();
    private readonly HashSet<string> seen = new HashSet<string>();
    private bool busy, completed, closing, loaded;
    private int unchanged, ticks;
    private string Host => request.provider == "douyin" ? "douyin.com" : request.provider == "bilibili" ? "bilibili.com" : "youtube.com";

    internal SearchWindow(string root, string id, bool comments = false)
    {
        this.root = root;
        requestID = id;
        commentMode = comments;
        requestDir = Path.Combine(root, comments ? "comment-requests" : "discovery-requests", id);
        var input = File.ReadAllBytes(Path.Combine(requestDir, "request.json"));
        if (input.Length > 4096) throw new InvalidDataException();
        request = json.Deserialize<SearchRequest>(Encoding.UTF8.GetString(input));
        if (request == null || (comments ? request.provider != "douyin" || !System.Text.RegularExpressions.Regex.IsMatch(request.video_id ?? "", "^[0-9]{10,30}$") : !new[] { "douyin", "bilibili", "youtube" }.Contains(request.provider) || string.IsNullOrWhiteSpace(request.query) || request.query.Length > 200)) throw new InvalidDataException();
        // Browser storage never contains other Windows accounts or browser profiles.
        var profileRoot = Path.Combine(root, "discovery-browser");
        Directory.CreateDirectory(profileRoot);
        var acl = new DirectorySecurity();
        acl.SetAccessRuleProtection(true, false);
        acl.AddAccessRule(new FileSystemAccessRule(WindowsIdentity.GetCurrent().User, FileSystemRights.FullControl, InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit, PropagationFlags.None, AccessControlType.Allow));
        new DirectoryInfo(profileRoot).SetAccessControl(acl);
        foreach (var candidate in Directory.GetDirectories(profileRoot, "browser-*")) {
            var match = System.Text.RegularExpressions.Regex.Match(Path.GetFileName(candidate), "^browser-([0-9]+)-[a-f0-9]{32}$");
            if (!match.Success) continue;
            try { using (var process = Process.GetProcessById(int.Parse(match.Groups[1].Value))) { if (!process.HasExited) continue; } }
            catch (ArgumentException) { }
            catch { continue; }
            try { Directory.Delete(candidate, true); } catch { }
        }
        profile = Path.Combine(profileRoot, "browser-" + Process.GetCurrentProcess().Id + "-" + id);
        Text = comments ? "官网评论 · 媒体资源库" : "官网搜索 · 媒体资源库";
        if (comments) status.Text = "正在读取官网公开评论；如出现验证，请在此窗口完成。";
        Width = 1100; Height = 800; MinimumSize = new System.Drawing.Size(860, 600);
        Font = new System.Drawing.Font("Microsoft YaHei UI", 9);
        StartPosition = FormStartPosition.CenterScreen;
        Opacity = 0; ShowInTaskbar = false;
        var actions = new FlowLayoutPanel { Dock = DockStyle.Bottom, Height = 50, Padding = new Padding(12, 8, 12, 8), FlowDirection = FlowDirection.RightToLeft };
        var cancel = new Button { Text = comments ? "取消读取" : "取消搜索", AutoSize = true };
        cancel.Click += (s, e) => Finish("canceled");
        actions.Controls.Add(cancel);
        Controls.Add(browser); Controls.Add(status); Controls.Add(actions);
        Shown += async (s, e) => await Initialize();
        timer.Tick += async (s, e) => await Poll();
        FormClosing += async (s, e) => {
            if (closing) return;
            e.Cancel = true; closing = true; timer.Stop();
            if (!completed) WriteResult("canceled");
            try {
                if (browser.CoreWebView2 != null) {
                    browser.CoreWebView2.CookieManager.DeleteAllCookies();
                    await browser.CoreWebView2.Profile.ClearBrowsingDataAsync();
                }
            } catch { }
            browser.Dispose();
            // Only this invocation's profile is cleaned; no global browser is touched.
            for (int i = 0; i < 12; i++) {
                try { if (Directory.Exists(profile)) Directory.Delete(profile, true); break; }
                catch { await Task.Delay(200); }
            }
            Close();
        };
    }

    private bool Official(string value)
    {
        return Uri.TryCreate(value, UriKind.Absolute, out var uri) && uri.Scheme == "https" && uri.Port == 443 && string.IsNullOrEmpty(uri.UserInfo) &&
            (uri.Host.Equals(Host, StringComparison.OrdinalIgnoreCase) || uri.Host.EndsWith("." + Host, StringComparison.OrdinalIgnoreCase));
    }

    private bool Current()
    {
        if (request.provider != "douyin") return true;
        try {
            var sessionRoot = Path.Combine(root, "site-sessions", "douyin");
            return !File.Exists(Path.Combine(sessionRoot, "expired")) && File.ReadAllText(Path.Combine(sessionRoot, "active")).Trim() == request.generation;
        } catch { return false; }
    }

    private async Task Initialize()
    {
        try {
            if (!Current()) { Finish("changed"); return; }
            if (File.Exists(Path.Combine(root, "discovery-cancel", requestID))) { Finish("canceled"); return; }
            var environment = await CoreWebView2Environment.CreateAsync(null, profile);
            await browser.EnsureCoreWebView2Async(environment);
            var core = browser.CoreWebView2;
            core.Settings.AreDevToolsEnabled = false;
            core.Settings.AreDefaultContextMenusEnabled = false;
            core.Settings.IsWebMessageEnabled = false;
            core.Settings.AreHostObjectsAllowed = false;
            core.IsMuted = true;
            core.PermissionRequested += (s, e) => e.State = CoreWebView2PermissionState.Deny;
            core.DownloadStarting += (s, e) => e.Cancel = true;
            core.NavigationStarting += (s, e) => { if (!Official(e.Uri)) e.Cancel = true; loaded = false; };
            core.NewWindowRequested += (s, e) => { e.Handled = true; if (Official(e.Uri)) core.Navigate(e.Uri); };
            core.NavigationCompleted += (s, e) => { loaded = e.IsSuccess; };
            if (request.provider == "douyin") RestoreOwnedSession(core);
            if (!Current()) { Finish("changed"); return; }
            if (File.Exists(Path.Combine(root, "discovery-cancel", requestID))) { Finish("canceled"); return; }
            string query = Uri.EscapeDataString(request.query ?? "");
            core.Navigate(commentMode ? "https://www.douyin.com/video/" + request.video_id : request.provider == "douyin" ? "https://www.douyin.com/search/" + query + "?type=video" :
                request.provider == "bilibili" ? "https://search.bilibili.com/video?keyword=" + query : "https://www.youtube.com/results?search_query=" + query);
            timer.Start();
        } catch { Finish("unavailable"); }
    }

    private void RestoreOwnedSession(CoreWebView2 core)
    {
        if (!System.Text.RegularExpressions.Regex.IsMatch(request.generation ?? "", "^[a-f0-9]{32}$")) throw new InvalidDataException();
        var sealedData = File.ReadAllBytes(Path.Combine(root, "site-sessions", "douyin", "session-" + request.generation + ".bin"));
        if (sealedData.Length > 1048576) throw new InvalidDataException();
        var clear = ProtectedData.Unprotect(sealedData, Encoding.UTF8.GetBytes("com.himind.media-library/douyin/v1"), DataProtectionScope.CurrentUser);
        try {
            var session = json.Deserialize<OwnedSession>(Encoding.UTF8.GetString(clear));
            if (session.site != "douyin" || session.cookies == null || session.cookies.Length > 500) throw new InvalidDataException();
            foreach (var cookie in session.cookies) {
                var domain = (cookie.domain ?? "").TrimStart('.');
                if (!(domain.Equals("douyin.com", StringComparison.OrdinalIgnoreCase) || domain.EndsWith(".douyin.com", StringComparison.OrdinalIgnoreCase)) || string.IsNullOrEmpty(cookie.name) || string.IsNullOrEmpty(cookie.value)) continue;
                if (cookie.expires != 0 && cookie.expires < DateTimeOffset.UtcNow.ToUnixTimeSeconds()) continue;
                var value = core.CookieManager.CreateCookie(cookie.name, cookie.value, cookie.domain, string.IsNullOrEmpty(cookie.path) ? "/" : cookie.path);
                value.IsSecure = true;
                if (cookie.expires != 0) value.Expires = DateTimeOffset.FromUnixTimeSeconds(cookie.expires).UtcDateTime;
                core.CookieManager.AddOrUpdateCookie(value);
            }
        } finally { Array.Clear(clear, 0, clear.Length); }
    }

    private async Task Poll()
    {
        if (busy || closing || completed) return;
        busy = true;
        try {
            if (File.Exists(Path.Combine(root, "discovery-cancel", requestID))) { Finish("canceled"); return; }
            if (!Current()) { Finish("changed"); return; }
            if (commentMode) { await PollComments(); return; }
            if (DateTime.UtcNow - started > TimeSpan.FromSeconds(54)) { Finish(cards.Count > 0 ? "ok" : "unavailable"); return; }
            if (!loaded) { RevealIfNeeded(); return; }
            ticks++;
            var script = request.provider == "youtube" ? YoutubeScript : request.provider == "bilibili" ? BilibiliScript : DouyinScript;
            var found = json.Deserialize<SearchDOM>(await browser.CoreWebView2.ExecuteScriptAsync(script));
            if (closing || completed || !Current()) return;
            int before = cards.Count;
            if (found?.items != null) foreach (var card in found.items.Take(120)) {
                if (Official(card.source_url) && seen.Add(card.source_url)) cards.Add(card);
                if (cards.Count >= 60) break;
            }
            unchanged = cards.Count == before ? unchanged + 1 : 0;
            if (found != null && found.empty && cards.Count == 0 && ticks >= 3) { Finish("empty"); return; }
            if (cards.Count >= 48 || (cards.Count > 0 && unchanged >= 4 && ticks >= 8)) { Finish("ok"); return; }
            await browser.CoreWebView2.ExecuteScriptAsync(ScrollScript);
            RevealIfNeeded();
        } catch { RevealIfNeeded(); }
        finally { busy = false; }
    }

    private void RevealIfNeeded()
    {
        if (cards.Count == 0 && DateTime.UtcNow - started > TimeSpan.FromSeconds(10) && !closing) { Opacity = 1; ShowInTaskbar = true; }
    }
    private void WriteResult(string state)
    {
        completed = true;
        try {
            var target = Path.Combine(requestDir, "result.json");
            var temp = Path.Combine(requestDir, "result.tmp");
            File.WriteAllText(temp, commentMode ? CommentResult(state) : json.Serialize(new { state, items = state == "ok" ? cards.Take(60).ToArray() : new SearchCard[0] }), new UTF8Encoding(false));
            File.Move(temp, target);
        } catch { }
    }
    private void Finish(string state) { if (closing || completed) return; WriteResult(state); Close(); }

    private const string YoutubeScript = @"(() => ({items:Array.from(document.querySelectorAll('ytd-video-renderer a#video-title[href], ytd-rich-item-renderer a#video-title-link[href]')).slice(0,120).map(a=>({source_url:a.href,title:a.getAttribute('title')||a.textContent||'',author:a.closest('ytd-video-renderer,ytd-rich-item-renderer')?.querySelector('ytd-channel-name')?.textContent||'',preview_url:a.closest('ytd-video-renderer,ytd-rich-item-renderer')?.querySelector('img')?.src||''})),empty:!!document.querySelector('ytd-message-renderer .ytd-message-renderer')}))()";
    private const string BilibiliScript = @"(() => ({items:Array.from(document.querySelectorAll('a[href*=""/video/BV""],a[href*=""/video/av""]')).slice(0,120).map(a=>{const c=a.closest('.bili-video-card,.video-item')||a;return {source_url:a.href,title:c.querySelector('h3')?.getAttribute('title')||c.querySelector('h3')?.textContent||a.getAttribute('title')||a.textContent||'',author:c.querySelector('.bili-video-card__info--author,.up-name')?.textContent||'',preview_url:c.querySelector('img')?.src||''}}),empty:!!document.querySelector('.search-nothing,.search-empty')}))()";
    private const string DouyinScript = @"(() => ({items:Array.from(document.querySelectorAll('a[href*=""/video/""],a[href*=""modal_id=""]')).slice(0,120).map(a=>{const c=a.closest('li,[data-e2e=""search-result-item""]')||a;const lines=(c.innerText||a.innerText||'').split(/\r?\n/).map(s=>s.trim()).filter(Boolean);const caption=lines.find(s=>s.includes('#')&&!s.startsWith('@'))||lines.filter(s=>!/^@|^(?:合集|\d{1,2}:\d{2}|[\d.]+[万亿wW]?|.*(?:小时前|天前|分钟前))$/.test(s)).sort((x,y)=>y.length-x.length)[0]||'';return {source_url:a.href,title:c.querySelector('[data-e2e=""video-desc""]')?.innerText||a.getAttribute('title')||c.querySelector('img')?.alt||caption,author:c.querySelector('[data-e2e=""video-author""]')?.innerText||lines.find(s=>s.startsWith('@'))||'',preview_url:c.querySelector('img')?.src||''}}),empty:!!document.querySelector('[data-e2e=""search-empty""]')}))()";
    private const string ScrollScript = @"(() => {const nodes=[document.scrollingElement,...document.querySelectorAll('main,[data-e2e=""scroll-list""],[class*=""scroll""]')].filter(n=>n&&n.clientHeight>100&&n.scrollHeight>n.clientHeight+10);nodes.sort((a,b)=>b.clientHeight-a.clientHeight);const n=nodes[0];if(n)n.scrollBy(0,n.clientHeight*.85);})()";

    private sealed class SearchRequest { public string provider { get; set; } public string query { get; set; } public string generation { get; set; } public string video_id { get; set; } }
    private sealed class SearchDOM { public SearchCard[] items { get; set; } public bool empty { get; set; } }
    private sealed class SearchCard { public string preview_url { get; set; } public string source_url { get; set; } public string title { get; set; } public string author { get; set; } }
    private sealed class OwnedSession { public string site { get; set; } public OwnedCookie[] cookies { get; set; } }
    private sealed class OwnedCookie { public string name { get; set; } public string value { get; set; } public string domain { get; set; } public string path { get; set; } public long expires { get; set; } }
}

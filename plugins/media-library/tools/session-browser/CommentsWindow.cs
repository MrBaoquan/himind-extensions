using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Threading.Tasks;

internal sealed partial class SearchWindow
{
    private readonly bool commentMode;
    private readonly List<PublicComment> comments = new List<PublicComment>();
    private readonly HashSet<string> commentKeys = new HashSet<string>();
    private string commentScope = "official_visible_sample";
    private int commentRoots;
    private string[] commentMarkers = new string[0];
    private static string CommentScript {
        get { var assembly = Assembly.GetExecutingAssembly(); using (var stream = assembly.GetManifestResourceStream(assembly.GetManifestResourceNames().Single(n => n.EndsWith("CommentsDOM.js")))) using (var reader = new StreamReader(stream)) return reader.ReadToEnd(); }
    }
    private async Task PollComments()
    {
        if (!Uri.TryCreate(browser.CoreWebView2.Source, UriKind.Absolute, out var currentURI) || currentURI.AbsolutePath.TrimEnd('/') != "/video/" + request.video_id) { Finish("changed"); return; }
        if (DateTime.UtcNow - started > TimeSpan.FromSeconds(50)) { Finish(comments.Count > 0 ? "ok" : "unavailable"); return; }
        if (!loaded) { RevealIfNeeded(); return; }
        ticks++;
        var found = json.Deserialize<CommentDOM>(await browser.CoreWebView2.ExecuteScriptAsync(CommentScript));
        if (found?.markers != null) commentMarkers = found.markers.Take(100).ToArray();
        if (closing || completed || !Current()) return;
        if (found != null && found.challenge) { RevealIfNeeded(); return; }
        int before = comments.Count;
        if (found?.items != null) foreach (var item in found.items.Take(160)) {
            if (string.IsNullOrWhiteSpace(item.text) || string.IsNullOrWhiteSpace(item.author)) continue;
            if (string.IsNullOrEmpty(item.key)) continue;
            if (!commentKeys.Add(item.key)) continue;
            comments.Add(item); if (string.IsNullOrEmpty(item.parent_key)) commentRoots++;
            if (comments.Count >= 120) break;
        }
        unchanged = comments.Count == before ? unchanged + 1 : 0;
        if (found != null && found.empty && comments.Count == 0 && ticks >= 4) { Finish("empty"); return; }
        if (commentRoots >= 60 || comments.Count >= 120 || (comments.Count > 0 && unchanged >= 4 && ticks >= 8)) { Finish("ok"); return; }
        RevealIfNeeded();
    }
    private string CommentResult(string state) => json.Serialize(new { state, scope = commentScope, markers = commentMarkers, items = state == "ok" ? comments.ToArray() : new PublicComment[0] });
    private sealed class CommentDOM { public PublicComment[] items { get; set; } public bool empty { get; set; } public bool challenge { get; set; } public string[] markers { get; set; } }
    private sealed class PublicComment {
        public string key { get; set; }
        public string parent_key { get; set; }
        public string author { get; set; }
        public string text { get; set; }
        public string published { get; set; }
        public string likes { get; set; }
        public string avatar_url { get; set; }
        public bool is_author { get; set; }
    }
}

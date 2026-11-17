using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Microsoft.Web.WebView2.Core;
using Microsoft.Web.WebView2.WinForms;

// Only the official site is rendered. No JS bridge, Cookie text or arbitrary URL
// is exposed to the caller. The profile belongs exclusively to this plugin.
internal static class Program
{
    [STAThread]
    private static void Main(string[] args)
    {
        if (args.Length != 2 || !Path.IsPathRooted(args[0]) || !System.Text.RegularExpressions.Regex.IsMatch(args[1], "^[a-f0-9]{32}$")) return;
        // Host WebView2 overrides take precedence over CreateAsync arguments.
        // Never inherit its profile, debugging flags or browser executable.
        foreach (System.Collections.DictionaryEntry entry in Environment.GetEnvironmentVariables())
            if (((string)entry.Key).StartsWith("WEBVIEW2_", StringComparison.OrdinalIgnoreCase)) Environment.SetEnvironmentVariable((string)entry.Key, null);
        var root = Path.GetFullPath(args[0]);
        var mutexName = "Local\\HiMind.MediaSession." + BitConverter.ToString(SHA256.Create().ComputeHash(Encoding.UTF8.GetBytes(root.ToLowerInvariant()))).Replace("-", "");
        using (var mutex = new Mutex(false, mutexName))
        {
            bool acquired;
            try { acquired = mutex.WaitOne(TimeSpan.FromSeconds(5)); } catch (AbandonedMutexException) { acquired = true; }
            if (!acquired) return;
            try
            {
                Application.EnableVisualStyles();
                Application.SetCompatibleTextRenderingDefault(false);
                Application.Run(new ConnectWindow(root, args[1]));
            }
            finally { mutex.ReleaseMutex(); }
        }
    }
}

internal sealed class ConnectWindow : Form
{
    private readonly string root, attempt, profile;
    private readonly JavaScriptSerializer json = new JavaScriptSerializer { MaxJsonLength = 1048576 };
    private readonly WebView2 browser = new WebView2 { Dock = DockStyle.Fill };
    private readonly Label status = new Label { Dock = DockStyle.Top, Height = 52, Padding = new Padding(16, 10, 16, 4), Text = "正在打开抖音官网…" };
    private readonly Button finish = new Button { Text = "完成连接", AutoSize = true, Enabled = false };
    private readonly System.Windows.Forms.Timer timer = new System.Windows.Forms.Timer { Interval = 1500 };
    private readonly DateTime deadline = DateTime.UtcNow.AddMinutes(10);
    private bool busy, saved, closing;
    private readonly bool requireLogin;
    private DateTime loadedAt = DateTime.MaxValue;

    public ConnectWindow(string root, string attempt)
    {
        this.root = root; this.attempt = attempt;
        profile = Path.Combine(root, "browser-" + Process.GetCurrentProcess().Id + "-" + attempt);
        requireLogin = File.Exists(Path.Combine(root, "expired"));
        Text = "连接抖音 · 媒体下载工具";
        Width = 1080; Height = 780; MinimumSize = new System.Drawing.Size(860, 600);
        StartPosition = FormStartPosition.CenterScreen;
        Font = new System.Drawing.Font("Microsoft YaHei UI", 9);
        var actions = new FlowLayoutPanel { Dock = DockStyle.Bottom, Height = 50, Padding = new Padding(12, 8, 12, 8), FlowDirection = FlowDirection.RightToLeft };
        var cancel = new Button { Text = "取消", AutoSize = true };
        cancel.Click += (s, e) => Close();
        finish.Click += async (s, e) => await CaptureSession(true);
        actions.Controls.Add(finish); actions.Controls.Add(cancel);
        Controls.Add(browser); Controls.Add(status); Controls.Add(actions);
        Shown += async (s, e) => await Initialize();
        timer.Tick += async (s, e) => { if (!Current() || DateTime.UtcNow > deadline) { Close(); return; } await CaptureSession(false); };
        FormClosed += (s, e) => { timer.Stop(); browser.Dispose(); if (!saved) WriteState("canceled", "连接已取消，可重新连接。"); };
        FormClosing += async (s, e) => {
            if (closing) return;
            e.Cancel = true; closing = true; timer.Stop();
            try {
                if (browser.CoreWebView2 != null) {
                    browser.CoreWebView2.CookieManager.DeleteAllCookies();
                    await browser.CoreWebView2.Profile.ClearBrowsingDataAsync();
                }
            } catch { }
            browser.Dispose();
            // WebView2 releases profile handles asynchronously. Never terminate
            // any browser process; an orphan is cleaned on the next status call.
            for (int i = 0; i < 12; i++) {
                try { if (Directory.Exists(profile)) Directory.Delete(profile, true); break; }
                catch { await Task.Delay(250); }
            }
            Close();
        };
    }

    private bool Current()
    {
        try { return File.ReadAllText(Path.Combine(root, "attempt")).Trim() == attempt; } catch { return false; }
    }
    private void WriteState(string state, string message)
    {
        if (!Current()) return;
        try { Atomic("connection.json", Encoding.UTF8.GetBytes(json.Serialize(new { state, message, attempt, updated_at = DateTime.UtcNow.ToString("o") }))); } catch { }
    }
    private void Atomic(string name, byte[] data)
    {
        var target = Path.Combine(root, name);
        var temp = target + "." + attempt + ".tmp";
        File.WriteAllBytes(temp, data);
        if (File.Exists(target)) File.Replace(temp, target, null); else File.Move(temp, target);
    }
    private static bool Official(Uri uri) => uri != null && uri.Scheme == "https" && uri.Port == 443 &&
        (uri.Host.Equals("douyin.com", StringComparison.OrdinalIgnoreCase) || uri.Host.EndsWith(".douyin.com", StringComparison.OrdinalIgnoreCase));

    private async Task Initialize()
    {
        var stage = "environment";
        try
        {
            // Separate profile; never open the user's Edge/Chrome profile.
            var env = await CoreWebView2Environment.CreateAsync(null, profile);
            stage = "control";
            await browser.EnsureCoreWebView2Async(env);
            stage = "settings";
            var core = browser.CoreWebView2;
            core.Settings.AreDevToolsEnabled = false;
            core.Settings.AreDefaultContextMenusEnabled = false;
            core.Settings.IsWebMessageEnabled = false;
            core.Settings.IsStatusBarEnabled = true;
            core.Settings.AreHostObjectsAllowed = false;
            core.NavigationStarting += (s, e) => { if (!Uri.TryCreate(e.Uri, UriKind.Absolute, out var uri) || !Official(uri)) e.Cancel = true; loadedAt = DateTime.MaxValue; };
            core.NewWindowRequested += (s, e) => { e.Handled = true; if (Uri.TryCreate(e.Uri, UriKind.Absolute, out var uri) && Official(uri)) core.Navigate(uri.AbsoluteUri); };
            core.PermissionRequested += (s, e) => e.State = CoreWebView2PermissionState.Deny;
            core.DownloadStarting += (s, e) => e.Cancel = true;
            core.NavigationCompleted += (s, e) => {
                if (e.IsSuccess) { loadedAt = DateTime.UtcNow; status.Text = requireLogin ? "上次会话未被站点接受，请在官网登录后返回；无需登录时也可点击“完成连接”使用当前会话。" : "请在抖音官网登录，完成后会自动返回下载工具；无需登录时也可点击“完成连接”。"; }
                else status.Text = "官网加载失败，请检查网络后点击下方重试。";
            };
            core.Navigate("https://www.douyin.com/");
            var retry = new Button { Text = "重新加载", AutoSize = true };
            retry.Click += (s, e) => core.Navigate("https://www.douyin.com/");
            ((FlowLayoutPanel)finish.Parent).Controls.Add(retry);
            timer.Start();
        }
        catch (Exception error) { saved = true; WriteState("failed", "连接窗口无法启动（" + stage + "/" + error.GetType().Name + "/" + error.HResult.ToString("X8") + "），请重试。"); Close(); }
    }

    private async Task CaptureSession(bool explicitFinish)
    {
        if (busy || closing || browser.CoreWebView2 == null || !Current()) return;
        busy = true;
        try
        {
            var cookies = await browser.CoreWebView2.CookieManager.GetCookiesAsync("https://www.douyin.com/");
            if (closing || !Current()) return;
            var now = DateTime.UtcNow;
            var selected = cookies.Where(c => (c.Domain.TrimStart('.').Equals("douyin.com", StringComparison.OrdinalIgnoreCase) || c.Domain.TrimStart('.').EndsWith(".douyin.com", StringComparison.OrdinalIgnoreCase)) &&
                !string.IsNullOrEmpty(c.Name) && !string.IsNullOrEmpty(c.Value) && (c.IsSession || c.Expires > now)).ToList();
            finish.Enabled = selected.Count > 0 && loadedAt != DateTime.MaxValue;
            // A guest identifier alone is often rejected by the extractor. Keep
            // the login window open until the user signs in, or explicitly elects
            // to use a guest session. Do not send them through an automatic loop.
            if (!finish.Enabled || (!explicitFinish && (!selected.Any(c => c.Name == "sessionid" || c.Name == "sessionid_ss") || DateTime.UtcNow - loadedAt < TimeSpan.FromSeconds(3)))) return;
            var payload = json.Serialize(new {
                site = "douyin", connected_at = DateTime.UtcNow.ToString("o"),
                cookies = selected.Select(c => new { name = c.Name, value = c.Value, domain = c.Domain, path = c.Path, secure = true, expires = c.IsSession ? 0L : new DateTimeOffset(c.Expires.ToUniversalTime()).ToUnixTimeSeconds() }).ToArray()
            });
            var clear = Encoding.UTF8.GetBytes(payload);
            var sealedData = ProtectedData.Protect(clear, Encoding.UTF8.GetBytes("com.himind.media-downloader/douyin/v1"), DataProtectionScope.CurrentUser);
            Array.Clear(clear, 0, clear.Length);
            if (closing || !Current()) return;
            // Each attempt has its own encrypted slot. Revocation/another attempt
            // can never be undone by a late result from this window.
            Atomic("session-" + attempt + ".bin", sealedData);
            WriteState("connected", "抖音已连接，解析和下载将自动复用。");
            saved = true; Close();
        }
        catch { status.Text = "暂时无法保存会话，请稍后点击“完成连接”重试。"; }
        finally { busy = false; }
    }
}

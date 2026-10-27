---
name: unihper-unity-development
description: 指导基于 UNIHper 的 Unity 项目开发，覆盖生命周期、UI、资源、UConfig、Managements、待机、UniRx 自闭环与 MCP 自动化测试。
---

# UNIHper 开发助手

以目标 Unity 工程实际使用的 UNIHper 源码、包版本和配置为准。UNIHper 的事实优先于本技能示例；实际源码不同时，以源码为准，并在交付结果中说明差异。

## 命名硬约束

- 框架品牌、程序集名和命名空间统一写作 `UNIHper`。
- 框架入口统一写作 `UNIHperEntry`。
- 设置类型统一写作 `UNIHperSettings`。
- 日志类型统一写作 `UNIHperLogger`。
- 程序集推荐写作 `<产品>.Integration.UNIHper`、`<产品>.Integration.UNIHper.Editor`。
- 命名空间推荐写作 `<产品>.Integration.UNIHper`。
- 文件名推荐写作 `<产品>UNIHperSettings.cs`、`<产品>UNIHperBridge.cs`、`<产品>UNIHperAssemblyRegistrar.cs`。
- 不使用任何大小写变体；框架相关标识只写 `UNIHper`。
- UPM 包 ID `com.parful.unihper` 是 registry 强制的小写稳定标识，可以保留小写；除该协议标识外，不要用错大小写。
- 技能稳定目录名 `unihper-unity-development` 是小写 ASCII 标识，不属于框架 API 命名。

## 开始前检查

1. 确认目标是 Unity 工程，定位 `Packages/manifest.json`、`Assets`、嵌入包或 Package Cache 中的 UNIHper 源码。
2. 读取实际 `package.json`、`UNIHperEntry`、`UNIHperSettings`、`SceneScriptManager`、`ConfigManager`、`ResourceManager`、`UIManager`、`Managements` 与相关 `.asmdef`。
3. 检查 `Assets/Resources/UNIHper/assemblies.json` 和 `Assets/Resources/UNIHper/resources.json`，不要把路径写成 `Configs`。
4. 记录 UNIHper 的真实版本；Git URL 安装时记录 ref、tag 或 commit，无法固定版本时说明可复现性风险。
5. 修改前列出目标文件；不修改 `Library`、`Temp`、`Logs`、导入缓存、第三方包缓存或凭据。
6. 若目标工程的 API、路径、程序集名或生命周期与本技能不同，停止套用示例，先核对源码。

当前知识基线是 UNIHper `1.25.1108`：程序集名为 `UNIHper`，命名空间为 `UNIHper`，最低声明 Unity 版本为 `2019.1`，依赖 Input System、Addressables、Mathematics、TextMeshPro 与 Newtonsoft Json。

## 一、架构总览

### UNIHperEntry

`UNIHperEntry` 是框架常驻启动器，负责音频、程序集扫描、配置、资源、UGUI、UI Toolkit、定时器、网络和场景脚本初始化。它不是业务功能容器。

### SceneEntryScript

`SceneEntryScript` 不是框架特殊基类，只是场景 `SceneEntry` 按命名约定对应的 `SceneScriptBase` 实现。任何场景的入口都是 `<SceneName>Script`。

```text
场景 SceneEntry
→ 查找 SceneEntryScript
→ 创建 SceneScriptBase 普通 C# 对象
```

### 依赖方向

```text
配置 / 资源
→ 场景脚本
→ UI
→ 业务适配层
```

UI 不承担资源生命周期，场景脚本不承担全局持久状态，配置类不访问其他管理器。

## 二、真实启动与场景生命周期

### 首次启动

```text
UNIHperEntry.Awake
→ 等待一帧
→ AudioManager.Initialize
→ AssemblyConfig.Refresh
→ SceneManager.Awake
→ SceneScript.Awake
→ ConfigManager.Initialize
→ ResourceManager.Initialize
→ UIManager.Initialize
→ UIToolkitManager.Initialize
→ TimerManager / UNetManager.Initialize
→ SceneManager.Initialize
→ UIManager.OnEnterScene
→ SceneScript.Start
```

关键结论：

- `SceneScript.Awake()` 执行时，配置、资源和 UI 还没有初始化完成。
- `SceneScript.Start()` 才适合访问 `Managements.Config`、`Managements.Resource`、`Managements.UI`。
- `OnSceneReadyAsObservable()` 在 `Start()` 调用前已置为 `true`；在 `Start()` 中订阅会立即收到通知。
- `SceneScript.Update()` 由 `Observable.EveryUpdate()` 反射调用；没有逐帧需求时不要保留。

### 场景切换

```text
旧 SceneScript.OnDestroy
→ 卸载旧场景资源
→ 加载新场景资源
→ Unity 加载新场景
→ UIManager / UIToolkitManager.OnEnterScene
→ LoadSceneAsync 的 completed 回调
→ 新 SceneScript.Awake
→ 新 SceneScript.Start
→ OnNewSceneLoadedAsObservable
```

- `LoadSceneAsync(..., completed)` 的 `completed` 早于新场景脚本 `Awake/Start`，不能视为新场景业务已完全启动。
- 需要完全就绪时监听 `Managements.Scene.OnNewSceneLoadedAsObservable()`。
- 退出应用时 `OnApplicationQuit` 与 `OnDestroy` 不应依赖固定先后顺序，清理必须幂等。

## 三、选择实现位置

| 需求 | 放置位置 |
| --- | --- |
| 当前场景启动、页面编排、场景级订阅 | `<SceneName>Script : SceneScriptBase` |
| Prefab/Canvas 页面 | `UIBase` + `UIPage` |
| UXML/USS 页面 | `UIToolkitBase` + `UIToolkitPage` |
| 跨场景常驻数据 | `UConfig`、`Persistence` 资源或明确的服务对象 |
| 纯业务计算 | 独立普通 C# 服务/纯函数 |
| 设备、网络、外部 SDK 适配 | 独立边界适配层 |
| 跨模块通知 | `Managements.Event` 或明确的只读 Observable |

不要把业务状态塞进 UI，也不要把 UI 引用塞进配置类。

## 四、场景脚本规范

### 基本约束

- 类是 `<SceneName>Script`，场景名与类名大小写完全一致。
- 继承 `SceneScriptBase`，不要继承 `MonoBehaviour`。
- 不使用 `[SerializeField]`、`gameObject`、`transform` 或协程。
- 类必须位于已注册到 UNIHper 的程序集，且保留无参构造路径。
- 当前工程的程序集注册文件是 `Assets/Resources/UNIHper/assemblies.json`。
- `Assembly-CSharp` 通常已注册；独立 asmdef 需要显式登记程序集名。

### 生命周期

| 方法 | 用途 | 禁止事项 |
| --- | --- | --- |
| `Awake()` | 无依赖字段初始化 | 不访问配置、资源、UI、音频 |
| `Start()` | 场景业务入口 | 不做无边界阻塞等待 |
| `Update()` | 必须逐帧的工作 | 不轮询网络、设备、配置或 UI 状态 |
| `OnDestroy()` | 释放本场景状态 | 不依赖 Unity 对象仍然存在 |
| `OnApplicationQuit()` | 应用级收尾 | 不依赖与 `OnDestroy` 的固定顺序 |

当前版本不要依赖 `SceneScriptBase.OnLongTimeNoOperation()`；统一使用 `Managements.Framework`。

### 推荐形态

```csharp
using UniRx;
using UNIHper;

public sealed class SceneHallScript : SceneScriptBase
{
    private readonly CompositeDisposable _disposables = new();

    private void Start()
    {
        Managements.UI.Show<HallUI>();

        OnSceneReadyAsObservable()
            .Take(1)
            .Subscribe(_ => StartBusinessFlow())
            .AddTo(_disposables);
    }

    private void OnDestroy()
    {
        _disposables.Dispose();
    }

    private static void StartBusinessFlow()
    {
        // 纯编排；复杂逻辑委托给独立服务
    }
}
```

场景切换只使用 `Managements.Scene.LoadSceneAsync(...)`，不要直接调用 Unity 原生 `SceneManager.LoadScene`。

## 五、UI 规范

### 默认入口

```csharp
Managements.UI.Show<T>();
Managements.UI.Hide<T>();
Managements.UI.Get<T>();
Managements.UI.IsShowing<T>();
```

`Managements.UI` 是 UGUI 与 UI Toolkit 的统一门面。只有需要专用 API 时才访问 `Managements.UGUI` 或 `Managements.UIToolkit`。

### 生命周期

```text
OnLoaded
→ OnShowing
→ OnShown
→ OnHiding
→ OnHidden
```

- `OnLoaded()` 负责绑定页面引用和一次性初始化。
- 页面开始显示时，框架创建 `LifeCycleDisposables`。
- 不要在 `OnLoaded()` 中把订阅加入 `LifeCycleDisposables`，此时它可能为 `null`。
- 显示期间有效的订阅放在 `OnShowing()`，使用 `.AddTo(LifeCycleDisposables)`。
- `OnShown()` 适合依赖显示动画完成后的动作。
- `OnHidden()` 在隐藏完成时执行，`LifeCycleDisposables` 随后释放。
- `Get<T>()` 只获取已生成实例；不要把它当作创建命令。
- 页面配置需要同时核对场景、资源名、类型、Canvas、Order 与 InstID。

```csharp
using UniRx;
using UNIHper;
using UNIHper.UI;

[UIPage(Asset = "HallUI", Type = UIType.Normal, Scene = "SceneHall")]
public sealed class HallUI : UIBase
{
    protected override void OnLoaded()
    {
        // 只绑定引用，不在这里订阅 LifeCycleDisposables。
    }

    protected override void OnShowing()
    {
        Managements.Event
            .OnSomethingAsObservable()
            .Subscribe(UpdateView)
            .AddTo(LifeCycleDisposables);
    }
}
```

不要手工创建第二套 UGUI 根对象或 UI Toolkit 根文档，也不要混用两套页面管理器。

## 六、资源规范

资源配置按组组织：

- `Persistence`：跨场景常驻资源。
- 场景名组：随场景进入和离开加载释放。
- 自定义组：运行时通过追加 API 注册。
- 当前工程配置入口是 `Assets/Resources/UNIHper/resources.json`。

默认使用：

```csharp
Managements.Resource.Get<T>(key);
Managements.Resource.GetMany<T>(filter);
Managements.Resource.Exists<T>(key);
```

规则：

- 业务代码不直接调用 `Resources.Load`。
- 资源读取放在 `SceneScript.Start()` 或 UI `OnShowing()` 之后。
- 不要把所有资源都改成 `Persistence` 来规避生命周期问题。
- Addressable 资源由框架按场景释放，业务代码不要再次调用 `Addressables.Release`。
- 外部纹理、音频等 Observable 加载结果必须绑定明确生命周期。
- 外部 AssetBundle 使用 `AppendAssetBundle` 和对应卸载 API。
- 资源缺失要有降级或错误提示，不能只留下空引用异常。
- 资源键必须与场景名、类型名和配置完全一致。

## 七、配置规范

### UConfig 生命周期

```text
AssemblyConfig.Refresh
→ ConfigManager.Initialize
→ 反序列化
→ OnDeserialized
→ 所有配置加载完成后统一 OnLoaded
→ 应用退出或框架清理 OnUnloaded
```

- `OnDeserialized()` 负责迁移、字段修正和默认值补全。
- `OnLoaded()` 负责配置自身校验与整理。
- `OnLoaded()` 执行早于 ResourceManager 和 UIManager，不要访问其他管理器。
- `OnSerializing/OnSerialized/OnUnloaded` 只处理序列化边界和自身状态。
- `Priority` 只影响配置类初始化排序，不是多文件覆盖优先级。
- 配置类所在程序集必须加入 `Assets/Resources/UNIHper/assemblies.json`。
- 新字段必须有安全默认值；重命名、改类型、换格式或换路径必须提供迁移和备份方案。

### 配置可见性

| 类型 | 内容 | 暴露方式 |
| --- | --- | --- |
| 公开可运营配置 | 待机时长、界面缩放、音量、展项 ID、非安全开关 | 外部配置或运营设置页 |
| 开发调试配置 | 日志级别、Mock、内部超时、测试设备参数 | Development Build 或独立诊断配置 |
| 只读运行信息 | 版本、构建号、配置路径、当前场景 | 诊断面板只读 |
| 机密配置 | 密钥、Token、支付凭据、设备 Secret、注册令牌 | 环境变量、部署系统或安全存储 |

- 每个公开字段必须有默认值、范围、校验和失败策略。
- UI 不直接修改 `UConfig` 字段；使用“草稿 → 校验 → 原子应用 → 保存”。
- 公开配置必须允许现场灵活修改，但不能因此暴露内部状态或安全参数。
- 重新加载配置默认需要显式 `ReloadAndApply` 或重启；不要无提示地部分应用。
- 配置变更日志不得包含密钥、Token 或隐私数据。

## 八、Managements 入口

默认使用统一门面，不缓存 Manager 实例。

| 能力 | 入口 |
| --- | --- |
| 配置 | `Managements.Config` |
| 统一 UI | `Managements.UI` |
| UGUI 专用 | `Managements.UGUI` |
| UI Toolkit 专用 | `Managements.UIToolkit` |
| 资源 | `Managements.Resource` |
| 场景 | `Managements.Scene` |
| 场景脚本 | `Managements.SceneScript<T>()` |
| 网络 | `Managements.Network` |
| 音频 | `Managements.Audio` |
| 定时器 | `Managements.Timer` |
| 事件 | `Managements.Event` |
| 生命周期订阅 | `Managements.Disposable` 或本地 `CompositeDisposable` |
| 框架服务 | `Managements.Framework` |

禁止：

```csharp
private readonly ResourceManager _resource = ResourceManager.Instance;
private readonly UIManager _ui = UIManager.Instance;
```

## 九、UniRx 规范

- 能用数据流表达的状态，不用多个布尔字段手工维护。
- 对外暴露 `IObservable<T>` 或 `IReadOnlyReactiveProperty<T>`。
- `ReactiveProperty<T>` 保持私有或有明确的所有者。
- 场景脚本使用本地 `CompositeDisposable`，在 `OnDestroy` 释放。
- UI 使用 `LifeCycleDisposables`，在显示周期结束后自动释放。
- 普通 MonoBehaviour 使用 `.AddTo(this)`，并明确其生命周期。
- 禁止裸 `Subscribe()`；所有订阅必须有所有者。
- 优先 `Where`、`Select`、`ThrottleFirst`、`Debounce`、`DistinctUntilChanged`、`CombineLatest`、`Switch`。
- 不用 `Update()` 做简单定时轮询；使用 `Managements.Timer` 或 Rx 时间流。
- 异步操作必须有超时、取消和异常处理。
- 简单同步流程不要强行 Rx 化；Rx 用于事件、状态、异步组合和取消。

## 十、自闭环与成员变量

默认局部变量、纯函数和最小副作用。成员变量只用于必须跨帧或对外观察的状态。

允许保留：

- `[SerializeField]` 的 Unity 引用。
- `CompositeDisposable`。
- `CancellationTokenSource`。
- `SerialDisposable`。
- 有明确生命周期的 `ReactiveProperty<T>`。
- 有失效条件和清理路径的缓存。
- 不可推导且必须跨帧共享的业务状态。

禁止保留：

- Manager 实例缓存。
- 临时计算字段。
- 可从现有状态推导的布尔值。
- 页面之间的直接引用。
- 场景资源的长生命周期缓存。
- 无清理路径的静态 List/Dictionary。
- 仅为了少传参数而创建的可变字段。

判断口诀：

```text
能局部就局部
能推导就推导
能 Observable 就不手工轮询
能单一状态机就不堆布尔值
必须成为成员时，同时写清所有权与释放路径
```

## 十一、长时间无操作与待机

UNIHper 的 `Framework` 持有一个全局 `LongTimeNoOperation`：

- 超时时间来自 `AppConfig.LongTimeNoOperationTimeout`。
- `Managements.Framework.OnLongTimeNoOperationAsObservable()` 观察超时。
- `Managements.Framework.OnResetLongTimeOperationAsObservable()` 观察恢复。
- `Managements.Framework.SetLongTimeNoOperationTimeout()` 可运行时调整。
- `Managements.Framework.ResetLongTimeNoOperation()` 可手动重置。

待机规则：

- 待机是应用级状态，使用单一 `AppMode` 或等价状态机，不使用多个布尔字段。
- 全局待机由常驻协调器负责，场景级待机才由 `<SceneName>Script` 负责。
- 进入待机时按固定顺序：防止重入、取消临时操作、关闭弹窗、停止媒体、清理临时状态、显示待机页、保留已提交业务数据。
- 恢复时重新检查网络、设备、订单和场景状态，成功后再切回活动态。
- 鼠标、键盘、触摸由框架自动重置；扫码、外部设备、业务点击等需要显式调用 `ResetLongTimeNoOperation()`。
- 心跳、轮询、后台同步和自动动画不算用户交互，不重置待机计时。
- 待机处理必须幂等，重复通知不能重复切页、销毁对象或保存配置。
- 支付、订单、设备动作等关键流程触发待机时，要保留服务端事务和对账路径，不能直接取消已提交事务。

## 十二、业务边界与可排查性

### 输入与状态

- 外部输入必须校验空值、范围、枚举、路径、URL 和设备状态。
- 复杂流程使用状态机，不堆布尔值。
- 非法状态转换明确拒绝并记录原因。
- UI 不直接调用网络或设备 SDK，业务逻辑放在适配层或服务层。

### 异步与并发

- 所有网络、设备、文件和支付操作必须有超时、取消和异常处理。
- 禁止 `.Result`、`.Wait()` 和无边界同步等待。
- 新请求覆盖旧请求时使用 `SerialDisposable`、`Switch` 或 `CancellationTokenSource`。
- 重复点击、重复扫码、重复回调和重试必须有业务幂等保护。
- 动画防抖和按钮禁用不能替代业务幂等。

### 日志与诊断

- 关键状态切换输出结构化日志：场景、模块、状态前后、原因、请求 ID、展项 ID、错误码。
- 不输出 Token、密钥、支付凭据、设备 Secret 或隐私数据。
- 启动和场景进入输出一条摘要：场景、配置版本、资源组、待机时长、构建版本。
- 诊断面板只读展示当前场景、UI 状态、配置路径、资源组、待机状态、最近错误和最近状态切换。
- 错误使用稳定错误码；用户看到可理解信息，日志保留技术细节。

### 失败边界

- 配置非法：关键项 fail-fast，非关键项回退默认值并告警。
- 资源缺失：明确降级或提示，不产生空引用异常。
- 网络断开：可重试、退避、超时和明确最终状态。
- 设备离线：心跳、重连和失效数据清理。
- 场景切换失败：保留可恢复状态，不能停留在半初始化状态。

## 十三、自动化开发与测试 harness

### 13.1 MCP 前置检查

涉及 Unity C#、场景、Prefab、资源、UPM 或 PlayMode 的自动化开发与测试时，先检查目标工程是否安装了 CoplayDev Unity MCP：

- 包名：`com.coplaydev.unity-mcp`
- 仓库：`https://github.com/CoplayDev/unity-mcp`
- 当前参考版本：`10.3.0`
- 最低 Unity：`2021.3`
- 依赖 Python `3.10+` 和 `uv`

如果工程没有安装：

1. 在 `Packages/manifest.json` 增加 Git URL 依赖。
2. 优先使用发布 tag 或固定 commit，不要长期直接依赖 `#main`。
3. 在 Unity 打开 `Window > MCP for Unity`，完成客户端配置并启动 Unity Bridge。
4. 确认 MCP 能查询 Editor 状态、读取 Console、执行测试，再开始自动化修改。

推荐固定版本写法：

```json
{
  "dependencies": {
    "com.coplaydev.unity-mcp": "https://github.com/CoplayDev/unity-mcp.git?path=/MCPForUnity#v10.3.0"
  }
}
```

注意：

- Unity 低于 `2021.3` 时不能使用当前 MCP，改用 Unity 批处理和 Test Runner。
- 工程已使用 `#main` 时提示可复现性风险，建议切换到明确发布 tag 或 commit；不要在未确认范围时擅自升级大版本。
- MCP 客户端配置、Token 和本机路径不写入项目仓库。
- 连接失败、Bridge 停止或 Tool 未加载时，不能假装自动化成功，必须降级并明确说明。

### 13.2 主流 harness 矩阵

| 目标 | 首选 | 兜底 |
| --- | --- | --- |
| Editor 状态、Play 模式、场景切换 | MCP `manage_editor` / `manage_scene` | Unity Editor 手工验证 |
| GameObject、Component、Prefab、Asset | MCP `manage_gameobject` / `manage_components` / `manage_prefabs` / `manage_asset` | 仅在无 MCP 时使用批处理或人工操作 |
| C# 与 ScriptableObject 修改 | MCP `manage_script` / `manage_scriptable_object` | 文本修改 + Unity 编译门禁 |
| Console 与异常检查 | MCP `read_console` | Editor Console 或 `-logFile` |
| EditMode / PlayMode 测试 | MCP `run_tests` + `get_test_job` | Unity CLI `-runTests` |
| 编译与资源导入 | MCP `refresh_unity` + `read_console` | Unity `-batchmode -quit` |
| 构建验证 | MCP `manage_build` | Unity CLI 构建入口 |
| UI 视觉冒烟 | MCP 截图/相机能力 + 目标设备检查 | 人工截图与真机检查 |
| 性能问题定位 | MCP `manage_profiler` | Unity Profiler |
| 包与依赖检查 | MCP `manage_packages` | `Packages/manifest.json` + Package Manager |
| 多步编辑 | MCP `batch_execute` | 分步执行并逐步读 Console |

`batch_execute` 只用于有界、可回滚的批量操作；每批后必须检查 Console，不能把几十个场景修改塞进一次无人审查的调用。

### 13.3 自动化工作流

1. **先读状态**：确认 Unity 版本、当前场景、Play 状态、Console、MCP Bridge 和 MCP 工具列表。
2. **再改代码或资产**：一次只处理一个目标模块；修改前记录文件、场景和资源范围。
3. **修改后刷新**：使用 `refresh_unity` 或触发 Unity 导入，再读取 Console。
4. **编辑模式测试**：先跑 EditMode；有失败先修，不进入 PlayMode。
5. **PlayMode 测试**：验证场景进入/退出、UI 显隐、待机、资源释放和订阅清理。
6. **构建验证**：需要发布时执行目标平台构建；构建失败必须阻断交付。
7. **视觉验证**：UI 或场景变化至少捕获一次目标画面；关键交互要真机或目标分辨率验证。
8. **结果落地**：记录测试平台、测试模式、失败用例、Console 错误、截图路径和未覆盖项。

### 13.4 最低测试门禁

- Unity 编译或脚本导入无 Error。
- EditMode 测试全绿。
- PlayMode 测试覆盖至少一条真实场景进入/退出链路。
- `read_console` 无未处理异常、Assert、NullReference、MissingReference。
- UI 首次显示、隐藏、再次显示无订阅残留。
- 待机进入/恢复、重复超时和有效输入恢复通过。
- 资源配置组和程序集注册无缺失。
- 涉及包或发布时，构建目标成功且产物可启动。
- 涉及设备、网络或支付时，补充目标环境或真机验证，不把 Editor 绿灯当成现场通过。

### 13.5 无 MCP 时的批处理兜底

固定使用目标工程锁定的 Unity Editor 版本：

```powershell
Unity.exe -batchmode -nographics -quit `
  -projectPath <工程路径> `
  -runTests -testPlatform EditMode `
  -testResults <EditMode结果.xml> `
  -logFile -
```

PlayMode 同理替换：

```text
-testPlatform PlayMode
```

批处理结果必须检查：

- 退出码
- `testResults` XML
- `-logFile` 中的编译错误、异常、Assert 和资源导入失败
- 是否存在因 License、Package Resolve、Domain Reload 导致的未执行测试

没有可执行的 Unity Editor、License 或测试结果时，明确报告“未验证”，不能根据文本修改推断通过。

### 13.6 自动化交付输出

至少列出：

- MCP 是否安装、版本和 Bridge 状态
- Unity Editor 版本
- 执行的编译、EditMode、PlayMode、构建和截图步骤
- 每个测试的通过/失败结果
- Console 错误与已修复问题
- 目标平台与未覆盖的真机/设备测试
- 修改文件、配置文件和程序集注册变化

## 十四、实施与验证

1. 先给出文件清单、模块归属和配置影响，再进行最小范围修改。
2. 保留 `.meta` 和 GUID；移动或改名资源时同步处理配套 `.meta`。
3. 所有 JSON 使用严格 JSON；检查场景名、类名、资源键和配置键完全一致。
4. 能用目标 Unity Editor 批处理编译时执行编译与测试；否则至少检查 C# 语法、asmdef、类型、命名空间、JSON、UXML 和 USS，并明确未执行 Unity 编译。
5. 验证场景进入/退出、UI 首次显示/重复显示/隐藏、资源配置组边界、配置首次生成/重载/迁移、待机进入/退出、重复输入、网络失败和设备离线。
6. 结果列出修改文件、验证命令与结果、版本差异、未覆盖的 Unity Editor 或设备测试。

## 十五、禁止事项

- 不把 `SceneScriptBase` 当 MonoBehaviour，不添加序列化引用或协程。
- 不绕过 `Managements.Scene` 完成框架管理的常规场景切换。
- 不混用 UGUI 与 UI Toolkit 页面根对象。
- 不猜测资源键、Addressable Label、Canvas、程序集或配置路径。
- 不把场景资源无条件改成 `Persistence`。
- 不在 UI 隐藏后遗留订阅。
- 不在 `OnLoaded` 中访问未初始化的管理器。
- 不依赖 `SceneScriptBase.OnLongTimeNoOperation()`。
- 不把密钥、Token 或凭据写入 `UConfig`、日志或场景文件。
- 不修改包缓存、生成目录、凭据或项目外文件。
- 不执行发布、上传或版本控制提交，除非用户明确要求并确认范围。
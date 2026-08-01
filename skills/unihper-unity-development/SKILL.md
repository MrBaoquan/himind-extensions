---
name: unihper-unity-development
description: 面向基于 UNIHper 框架的 Unity 项目开发，设计、实现、审查和排查场景脚本、UGUI、UI Toolkit、资源、配置及相关管理器；当用户提到 UNIHper、SceneScriptBase、UIBase、UIToolkitBase、ResourceManager、UConfig、Managements 门面或要求按 UNIHper 约定编写 Unity 功能时使用。
---

# UNIHper Unity 开发助手

以目标 Unity 工程的实际 UNIHper 版本和配置为准，完成符合框架生命周期与资源边界的实现。不要把本技能所在仓库或其他项目的业务约定带入目标工程。

## 开始前检查

1. 确认目标目录是 Unity 工程，并定位 `Packages/manifest.json`、嵌入包或 `Assets` 下的 UNIHper 源码。
2. 读取目标工程实际使用的 `package.json`、相关管理器源码、程序集定义和 `Assets/Resources/Configs`。若包来自 Git URL，记录 ref、tag 或 commit；没有固定版本时明确提示可复现性风险。
3. 优先遵守目标工程已有命名空间、asmdef、配置拆分和同类实现。仓库中存在 `AGENTS.md` 或项目说明时只把它们用于目标工程约束，不用它们改写 UNIHper 的框架事实。
4. 在修改前说明将触及的文件。不要改动 `Library`、`Temp`、`Logs`、导入缓存或第三方包缓存。
5. 若实际源码与本技能 API 不同，以实际源码为准，并在结果中指出版本差异。不要凭空补造重载、特性字段或生命周期方法。

本技能的知识基线来自 UNIHper `main` 分支、包版本 `1.25.1108`。该版本包名为 `com.parful.unihper`，最低声明 Unity 版本为 `2019.1`，并依赖 Input System、Addressables、Mathematics、TextMeshPro 和 Newtonsoft Json 等包。

## 选择工作流

- 创建或修改场景级逻辑：使用“场景脚本”。
- 创建 Prefab/Canvas 页面：使用“UGUI 页面”。
- 创建 UXML/USS 页面：使用“UI Toolkit 页面”。
- 注册或获取素材：使用“资源管理”。
- 创建持久化参数：使用“配置管理”。
- 跨模块功能：先确定数据所有者，再按“配置/资源 -> 场景 -> UI”的依赖方向实施，避免 UI 反向承担资源生命周期。

## 场景脚本

### 核心约定

- 每个场景使用 `<SceneName>Script` 类，例如 `SceneHall` 对应 `SceneHallScript`。
- 类继承 `SceneScriptBase`，不要继承 `MonoBehaviour`，不要挂到 GameObject。
- `SceneScriptManager` 通过 `AssemblyConfig.GetUNIType(sceneName + "Script")` 反射创建普通 C# 对象。确保类所在程序集已被 UNIHper 扫描；必要时检查 `Assets/Resources/Configs/assemblies.json` 或项目的程序集注册方式。
- 可声明私有 `Awake`、`Start`、`Update`、`OnDestroy`、`OnApplicationQuit`。这些名称由框架反射调用，不是 Unity 的 MonoBehaviour 消息。
- `Update` 由 `Observable.EveryUpdate()` 驱动；离开场景时框架会释放该订阅并调用 `OnDestroy`。
- `OnSceneReadyAsObservable()` 表示场景资源和 UI 进入流程已经完成。订阅必须纳入 `CompositeDisposable`，并在 `OnDestroy` 释放。
- 需要响应长期无操作时重写 `protected override void OnLongTimeNoOperation()`。

```csharp
using UniRx;
using UNIHper;

public sealed class SceneHallScript : SceneScriptBase
{
    private readonly CompositeDisposable _disposables = new();

    private void Awake()
    {
    }

    private void Start()
    {
        OnSceneReadyAsObservable()
            .Take(1)
            .Subscribe(_ => Managements.UI.Show<HallUI>())
            .AddTo(_disposables);
    }

    private void OnDestroy()
    {
        _disposables.Dispose();
    }
}
```

切换场景使用 `Managements.Scene.LoadSceneAsync(sceneName, progress, completed)`，不要直接绕过框架调用 Unity `SceneManager.LoadScene`，否则场景资源卸载/加载、场景 UI 进入和脚本销毁/启动链可能不完整。用 `OnNewSceneLoadedAsObservable()` 观察框架完成的新场景事件。

### 场景检查清单

- 场景名、类名和大小写完全一致。
- 类可被已注册程序集发现，且有无参构造路径。
- 不使用序列化字段、协程或 `gameObject` 等 MonoBehaviour 能力。
- 订阅、计时器和外部事件在 `OnDestroy` 中释放。
- 依赖管理器初始化的逻辑放到场景就绪之后。

## UGUI 页面

### 声明与生命周期

UGUI 页面继承 `UNIHper.UI.UIBase`，使用 `UIPage` 特性声明。先检查目标版本中 `UIPage` 的实际字段；常见字段包括 `Asset`、`Type`、`Order`、`Canvas` 和 `Scene`。

```csharp
using UniRx;
using UNIHper;
using UNIHper.UI;

[UIPage(Asset = "HallUI", Type = UIType.Normal, Order = 0, Scene = "SceneHall")]
public sealed class HallUI : UIBase
{
    private readonly CompositeDisposable _visibleDisposables = new();

    protected override void OnLoaded()
    {
        // 缓存子节点引用；这里只执行一次。
    }

    protected override void OnShown()
    {
        // 只在可见期间有效的订阅放在这里。
    }

    protected override void OnHidden()
    {
        _visibleDisposables.Clear();
    }
}
```

- `OnLoaded` 用于一次性引用缓存和初始化。
- `OnShowing/OnShown/OnHiding/OnHidden` 对应过渡前后；可通过四个 Observable 观察生命周期。
- 页面自身可调用 `Show()`、`Hide()`、`Toggle()`；外部优先使用 `Managements.UI.Show<T>()`、`Hide<T>()`、`Get<T>()`、`IsShowing<T>()`。
- `Get<T>()` 只获取已生成实例，不保证显示；不要把它当作创建命令。
- `UIType.Normal`、`Popup`、`Standalone` 决定挂载层和堆叠行为。弹窗关闭优先遵循当前页栈；批量切换可使用 `HideAll`、`StashActiveUI`、`PopStashedUI`。
- 需要动画时设置 `ShowDuration`、`HideDuration` 并重写目标版本提供的过渡方法；取消中的过渡必须允许框架接管。
- 配置式页面同时核对 `ui.json` 中场景键、脚本键、资源名、类型和 Canvas。特性注册与 JSON 注册共存时，避免重复键和相互覆盖。

## UI Toolkit 页面

UI Toolkit 与 UGUI 是并行管理器，不要混用 `Managements.UI` 与 `Managements.UIToolkit`。

```csharp
using UnityEngine.UIElements;
using UNIHper;
using UNIHper.UI;

[UIToolkitPage(Asset = "UI/SettingsUI", Type = UIType.Normal, Scene = "SceneHall")]
public sealed class SettingsUI : UIToolkitBase
{
    protected override void OnLoaded()
    {
        BindButton("close-button", () => Managements.UIToolkit.Hide<SettingsUI>());
    }
}
```

- `Asset` 指向 `VisualTreeAsset` 的框架资源键；先确保它已被资源配置加载。
- 在 `OnLoaded` 后使用 `Root`、`Q(name, className)`、`BindButton`、`BindTextField`、`BindToggle`、`BindSlider`。
- 页面由管理器创建 `UIDocument` 和容器；不要在业务代码中再创建第二套根文档。
- 显隐使用 `Managements.UIToolkit.Show<T>()`、`Hide<T>()`、`Get<T>()`、`GetOrCreate`、`Toggle`。只有明确需要释放实例时才调用 `Destroy`。
- 默认显隐时长为 0.3 秒；可重写 `HandleShowAnimation`、`HandleHideAnimation` 并尊重 `CancellationToken`。可使用 `UIToolkitAnimations` 的淡入、缩放和滑入动画。
- UXML 中的元素名必须与绑定代码一致；USS、字体、PanelSettings 与排序层问题要分别检查，不把样式错误误判为资源加载失败。

## 资源管理

### 配置模型

资源配置按组组织：`Persistence` 常驻，场景名组随场景进入/离开加载和释放，运行时追加资源进入自定义组。资源项常用字段如下：

```json
{
  "SceneHall": [
    { "driver": "Resources", "type": "GameObject", "path": "Prefabs/Hall" },
    { "driver": "Addressable", "type": "Sprite", "label": "scene_hall" },
    { "driver": "AssetBundle", "path": "hall.bundle" }
  ]
}
```

- `Resources`：`path` 相对 Resources，适合小型、随包资源。
- `Addressable`：以 `label` 批量加载，适合按场景或模块分组；资源必须实际加入 Addressables 并设置相同 Label。
- `AssetBundle`：路径相对框架约定的 StreamingAssets/AssetBundles 位置；核对目标源码的路径拼接规则。

配置入口通常是 `Assets/Resources/Configs/res.json`，框架还会合并持久层和通过 `AddConfig` 注册的配置。不要创建文档中出现但目标工程不存在的 `resources.json`；先以源码实际读取的文件名为准。

### 获取与生命周期

- 单个资源：`Managements.Resource.Get<T>(key)`。
- 模糊/批量匹配：`GetMany<T>(filter)`；可能返回多个同名项时不要依赖未声明顺序。
- 存在性：`Exists<T>(key)`。
- Addressable 标签结果：`GetLabelAssets<T>(label)`。
- 外部音频或纹理：使用 `AppendAudioClip(s)`、`AppendTexture2D(s)` 返回的 Observable，并管理订阅。
- 外部 AssetBundle：使用 `AppendAssetBundle`，不用后调用 `UnloadAssetBundle`。

资源键可能由名称或相对路径构成。遇到空结果时依次核查：配置组是否属于当前场景或 `Persistence`、类型字符串能否解析、Addressable Label 是否匹配、程序集是否可识别类型、资源键是否冲突、场景是否已经通过框架进入。不要通过把所有资源改成 `Persistence` 来掩盖生命周期问题。

## 配置管理

配置类继承 `UConfig`，用 `SerializedAt` 决定路径，用 `SerializeWith` 决定 XML 或 JSON。目标版本支持的 `AppPath` 与 `ConfigDriver` 必须从源码核对。

```csharp
using UNIHper;

[SerializedAt(AppPath.PersistentDir, "Configs", "UserSettings.json", Priority = 10)]
[SerializeWith(ConfigDriver.JSON)]
public sealed class UserSettings : UConfig
{
    public float Volume = 1f;

    protected override void OnLoaded()
    {
        Volume = UnityEngine.Mathf.Clamp01(Volume);
    }

    protected override string Comment() => "用户设置";
}
```

- `StreamingDir`、`PersistentDir`、`DataDir`、`ProjectDir` 的可写性和发布后语义不同。用户可变数据通常放 `PersistentDir`；不要尝试在发布包内写只读 StreamingAssets。
- 没有显式文件名时，框架按类名和驱动补后缀。`Priority` 影响同一配置类型的落点选择；先核对特性解析逻辑再叠加多个 `SerializedAt`。
- 获取：`Managements.Config.Get<T>()`；保存：`Save<T>()` 或实例 `Save()`；重载：`Reload<T>()`；批量保存：`SaveAll()`；需要恢复策略时检查 `Backup`、`BackupAll`。
- `OnDeserialized` 适合数据迁移/修正，`OnLoaded` 适合依赖已装载状态的初始化，`OnSerializing/OnSerialized/OnUnloaded` 分别处理序列化边界。不要在属性初始化器中依赖其他管理器。
- 新字段要给安全默认值。重命名/删除字段、切换 XML/JSON、变更路径或文件名属于数据迁移，必须说明兼容策略和备份方案。

## 其他框架能力

需要时通过 `Managements` 门面使用音频、事件、定时器、网络和框架服务。先查看目标工程中的同类代码和对应管理器公开 API。UniRx 订阅必须绑定到明确生命周期；普通场景脚本用 `CompositeDisposable`，UI 可按显示周期清理，MonoBehaviour 才使用 `AddTo(gameObject)`。

## 实施与验证

1. 先给出文件清单和模块归属，再进行最小范围修改。
2. 保留 `.meta` 文件和 GUID；移动 Unity 资源时同时处理配套 `.meta`，不要手工生成随机 GUID 冒充 Unity 导入结果。
3. JSON 使用严格 JSON，不保留示例注释。检查键名与场景名、类名、资源标签完全一致。
4. 能运行 Unity 批处理编译时，使用目标项目锁定的 Editor 版本；否则至少检查 C# 语法、asmdef 引用、类型/命名空间、JSON/UXML/USS 结构，并明确未执行 Unity 编译。
5. 对场景切换验证进入和退出各一次；对 UI 验证首次创建、重复显示、隐藏、再次显示和场景离开；对资源验证当前场景与 Persistence 边界；对配置验证首次生成、保存、重启重载和旧数据迁移。
6. 结果中列出实现摘要、修改文件、验证命令/结果、版本差异、未覆盖的 Unity Editor 或设备测试。

## 禁止事项

- 不把 `SceneScriptBase` 当 MonoBehaviour，不向其添加序列化引用或协程。
- 不绕过 `Managements.Scene` 完成框架管理场景的常规切换。
- 不混用 UGUI 与 UI Toolkit 管理器。
- 不猜测资源键、Addressable Label、Canvas 或程序集注册；先查目标配置。
- 不把场景资源无条件改为 Persistence，不在 UI 隐藏后遗留订阅。
- 不修改包缓存、生成目录、凭据或项目外文件，不执行发布、上传和版本控制提交，除非用户明确要求并确认范围。

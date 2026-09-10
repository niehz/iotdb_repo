# IoTDB ORM - 轻量级IoTDB操作库

为Apache IoTDB树形模型设计的轻量级ORM库，模仿GORM的API风格，基于官方客户端 `apache/iotdb-client-go` 实现。

## 特性

- 🚀 **高性能**: 使用Tablet批量写入，避免单条插入性能问题
- 🎯 **树形模型适配**: 专为IoTDB树形路径模型（`root.a.b.c`）设计
- 🔧 **GORM风格API**: 提供类似GORM的链式调用API
- 📊 **类型安全**: 完整的Go类型到IoTDB数据类型映射
- 🔄 **SessionPool管理**: 自动连接池管理
- 📝 **原生SQL支持**: 支持执行原生IoTDB SQL（`Raw`）
- 🏗️ **元数据管理**: 约定路径管理器（模板自动拼路径、show timeseries 自动发现设备），支持跨设备查询
- 🏷️ **双标签支持**: 支持 `iotdb` 与 `gorm` 标签解析，便于从GORM迁移
- 🧪 **完整测试**: 单元测试 + 基于内存后端的集成测试
- 🧫 **离线体验**: 提供内存mock后端，无需真实IoTDB服务即可运行示例

## 适用版本

### IoTDB 服务端

| 服务端版本 | 兼容性 | 说明 |
|-----------|--------|------|
| **1.3.x** | ✅ 官方支持 | 客户端 v1.3.7 与服务端 1.3.x 系列配套发布，本项目在本机 **1.3.1** 实测通过（建序列、Tablet批量写入、查询均正常） |
| 1.0 ~ 1.2.x | ⚠️ 理论兼容 | 基于 V3 会话协议（自 1.0 引入），协议层面兼容，但未实测验证 |
| 0.13 及更早 | ❌ 不支持 | 无 V3 会话协议 |
| 2.x | ❌ 不推荐 | 2.x 表模型应使用官方 2.0.x 客户端；本库为树形模型设计，未对 2.x 验证 |

**结论：推荐使用 Apache IoTDB 1.3.x（1.3.1 ~ 1.3.7 均在本库兼容范围内）。**

### 客户端依赖

- `github.com/apache/iotdb-client-go v1.3.7`（与服务端 1.3.x 配套的 1.3 系列客户端）
- Go 1.21+

### 已知服务端缺陷（已规避）

- **IoTDB 1.3.1**：SELECT 列表中显式包含 `time` 列（如 `SELECT time, temperature FROM root.x`）会导致服务端异常断开连接，客户端表现为 `EOF`。IoTDB 查询总是隐式返回 time 作为第一列，因此本库在生成 SQL 时会**自动过滤** Select 中的 time 列；原生 SQL（`Raw`）也请勿显式查询 time 列。更高版本是否已修复未逐一验证，本库行为在所有版本下一致安全。

## 安装

```bash
go get github.com/niehz/iotdb_repo
```

> 说明：**导入路径是 `github.com/niehz/iotdb_repo`，包名是 `iotdborm`**。
> 代码中 import 写全路径，调用时直接用包名 `iotdborm.xxx`，无需别名：

```go
import "github.com/niehz/iotdb_repo" // 包名为 iotdborm

repo := iotdborm.NewRepo(pool, "root.factory.workshop01.device01")
```

## 快速开始

### 定义数据结构

```go
import "github.com/niehz/iotdb_repo" // 包名 iotdborm

type DeviceMetric struct {
    Time        int64   `iotdb:"time"`        // 时间戳（int64毫秒），自动映射
    Temperature float64 `iotdb:"temperature"` // 测点名称
    Pressure    float64 `iotdb:"pressure"`
    Humidity    float64 `iotdb:"humidity"`
}
```

### 创建连接与仓库

```go
pool, err := iotdborm.NewSessionPool("127.0.0.1", 6667, "root", "root", 5)
if err != nil {
    log.Fatal(err)
}
defer pool.Close()

repo := iotdborm.NewRepo(pool, "root.factory.workshop01.device01")
```

### 初始化时间序列（首次运行）

```go
metadata, err := iotdborm.NewDeviceMetadata("root.factory.workshop01.device01", DeviceMetric{})
if err != nil {
    log.Fatal(err)
}
if err := repo.CreateTimeseries(metadata); err != nil {
    // 时间序列已存在时会报错，可忽略
}
```

### 写入数据

```go
// 单条写入
err = repo.Create(&DeviceMetric{
    Time:        time.Now().UnixMilli(),
    Temperature: 26.5,
    Pressure:    101.2,
    Humidity:    65.0,
})

// 批量写入（Tablet高性能写入）
var batch []DeviceMetric
for i := 0; i < 1000; i++ {
    batch = append(batch, DeviceMetric{
        Time:        time.Now().Add(time.Duration(i) * time.Second).UnixMilli(),
        Temperature: 25.0 + float64(i)*0.1,
    })
}
err = repo.CreateInBatches(batch, 100) // 每批100条
```

### 查询数据

```go
// 查询全部
var all []DeviceMetric
err = repo.Find(&all)

// 链式条件查询（Where支持 ? 占位符）
var recent []DeviceMetric
err = repo.
    Where("time >= ? AND time <= ?", time.Now().Add(-time.Hour).UnixMilli(), time.Now().UnixMilli()).
    Order("time", true).
    Limit(100).
    Find(&recent)

// 查询第一条（GORM风格，直接传结构体指针）
var first DeviceMetric
err = repo.Order("time", true).First(&first)

// 指定字段查询（无需选择time列，自动返回）
var temps []struct {
    Time        int64   `iotdb:"time"`
    Temperature float64 `iotdb:"temperature"`
}
err = repo.Select("temperature").Find(&temps)

// QueryBuilder 复杂查询
builder := iotdborm.NewQueryBuilder(repo)
var data []DeviceMetric
err = builder.
    Where("time >= ?", time.Now().Add(-time.Hour).UnixMilli()).
    Where("temperature < ?", 30.0).
    Or("pressure > ?", 105.0).
    Select("temperature", "pressure").
    Order("time", false).
    Limit(50).
    Find(&data)

// 原生SQL查询
rawSQL := "SELECT temperature FROM root.factory.workshop01.device01 WHERE time >= " +
    fmt.Sprintf("%d", time.Now().Add(-30*time.Minute).UnixMilli()) + " ORDER BY time DESC LIMIT 10"
var raw []DeviceMetric
err = repo.Raw(rawSQL, &raw)
```

### 设备元数据管理（跨设备查询）

**推荐：约定路径管理器**——路径由模板自动生成，标签即路径段，无需手工维护 deviceId↔路径 映射：

```go
type DeviceInfo struct {
    DeviceId string `iotdb:"device_path"`
    Region   string `gorm:"tag:region"`   // gorm:"tag:xxx" 填充模板中的 {xxx} 占位符
    Status   bool   `gorm:"tag:status"`
}

// 模板 root.factory.{region}.{deviceId}：region 是标签也是路径段
manager, err := iotdborm.NewConventionPathManager("root.factory.{region}.{deviceId}", pool)

// 注册只传设备信息，路径自动生成
err = manager.RegisterDeviceAuto(DeviceInfo{DeviceId: "d01", Region: "north", Status: true})

// 按标签筛选设备：直查IoTDB（show timeseries 前缀扫描），新设备无需注册即可见
deviceIds, err := manager.ListDevicesByTag("region", "north")

// 自动发现设备路径 / 一键同步存量设备
paths, err := manager.ListDevicePaths("root.factory.**")
n, err := manager.SyncDevices("root.factory.**")

// 带元数据管理的仓库：写入时自动解析路径，未注册的设备也能按模板零注册直写
repo := iotdborm.NewRepoWithMetadata(pool, "", manager)
err = repo.Create(&data{DeviceId: "d01", Region: "north", ...})
```

**路径无稳定规律时**，使用内存元数据管理器显式注册：

```go
manager := iotdborm.NewMemoryDeviceMetadataManager()
err = manager.RegisterDevice("root.factory.north.device01", DeviceInfo{DeviceId: "d01", Region: "north", Status: true})
deviceIds, err := manager.ListDevicesByTag("region", "north")
```

## 项目结构

```
iotdb_repo/                      # 库本体（module: github.com/niehz/iotdb_repo，包名 iotdborm）
├── go.mod
├── types.go                     # 类型映射与元数据结构
├── pool.go                      # Session/SessionPool/ResultSet 抽象 + 官方客户端适配
├── repo.go                      # MetricRepo 接口、链式API、CRUD
├── write.go                     # Tablet 批量写入、时间序列创建
├── query.go                     # 查询SQL构建、结果反射填充
├── reflect.go                   # 结构体标签解析（iotdb/gorm）
├── builder.go                   # QueryBuilder
├── metadata.go                  # 设备元数据管理器（内存实现）
├── convention.go                # 约定路径管理器（模板自动拼路径 + show timeseries 自动发现）
├── mock/                        # 内存后端（无需真实IoTDB）
├── iotdborm_test.go             # 单元测试
└── integration_test.go          # 基于mock的集成测试
examples/                        # 演示用例（独立module，通过 replace github.com/niehz/iotdb_repo => ../ 引入本库）
├── basic/                       # 完整生命周期演示（需真实IoTDB）
├── query/                       # QueryBuilder复杂查询演示（需真实IoTDB）
├── metadata/                    # 设备元数据管理演示（需真实IoTDB）
├── mock/                        # 离线演示（无需IoTDB）
└── diag/                        # 官方客户端诊断程序（排查服务端问题用）
```

## 运行示例

```bash
# 离线体验（无需IoTDB服务）
cd examples/mock
go run .

# 连接真实IoTDB（默认 127.0.0.1:6667 root/root）
cd examples/basic
go run . -host 127.0.0.1 -port 6667 -user root -password root
```

## 注意事项

1. **time列**: 不要在 Select 或原生SQL中显式查询 time 列（IoTDB 1.3.1 服务端缺陷会导致连接断开报EOF），time 列总是自动作为结果第一列返回；WHERE/ORDER BY 中使用 time 是安全的
2. **时间字段**: 结构体中 `Time int64`（毫秒）自动映射为IoTDB时间戳
3. **批量写入**: 强烈建议使用 `CreateInBatches`（Tablet批量），性能远优于单条 `Create`
4. **连接池**: SessionPool 全局复用，不要频繁创建销毁
5. **标签解析**: 优先 `iotdb` 标签，其次 `gorm` 标签（column/tag），最后使用字段名；`iotdb:"-"` 排除字段
6. **不支持的操作**: IoTDB 不支持 Update/Delete，调用会返回错误说明；事务接口为占位实现

## 许可证

MIT License

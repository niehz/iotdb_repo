# iotdborm

GORM 风格的 Apache IoTDB ORM 库，为 Go 开发者提供熟悉的数据访问接口。

## 功能特性

- **GORM 风格 API**：链式查询 `Where().Order().Find()`，零学习成本
- **结构体映射**：通过 `iotdb` / `gorm` 标签自动映射字段到 IoTDB 测点
- **高性能批量写入**：内部使用 Tablet API，支持自定义批大小
- **连接池管理**：内置 SessionPool，支持连接复用
- **查询构建器**：QueryBuilder 支持复杂条件组合
- **内存 Mock**：无需真实 IoTDB 即可运行示例和测试
- **元数据管理**：MemoryDeviceMetadataManager 支持设备注册和跨设备查询

## 安装

```bash
go get iotdborm
```

## 快速开始

```go
package main

import (
    "log"
    "time"

    "iotdborm"
)

// 定义设备数据结构
type DeviceMetric struct {
    Time        int64   `iotdb:"time"`
    Temperature float64 `iotdb:"temperature"`
    Pressure    float64 `iotdb:"pressure"`
    Humidity    float64 `iotdb:"humidity"`
}

func main() {
    // 1. 创建 SessionPool
    pool, err := iotdborm.NewSessionPool("127.0.0.1", 6667, "root", "root", 5)
    if err != nil {
        log.Fatal(err)
    }
    defer pool.Close()

    // 2. 创建 Repository，绑定设备路径
    devicePath := "root.factory.workshop01.device01"
    repo := iotdborm.NewRepo(pool, devicePath)

    // 3. 初始化时间序列
    metadata, _ := iotdborm.NewDeviceMetadata(devicePath, DeviceMetric{})
    repo.CreateTimeseries(metadata)

    // 4. 写入数据
    repo.Create(&DeviceMetric{
        Time:        time.Now().UnixMilli(),
        Temperature: 26.5,
        Pressure:    101.2,
        Humidity:    65.0,
    })

    // 5. 查询数据
    var results []DeviceMetric
    repo.Where("time >= ?", time.Now().Add(-time.Hour).UnixMilli()).
        Order("time", true).
        Limit(10).
        Find(&results)
}
```

## API 示例

### 条件查询

```go
var results []DeviceMetric
repo.Where("temperature > ?", 25.0).
    Or("pressure > ?", 100.0).
    Order("time", true).
    Limit(100).
    Offset(0).
    Find(&results)
```

### 指定字段查询

```go
var temps []struct {
    Time        int64   `iotdb:"time"`
    Temperature float64 `iotdb:"temperature"`
}
repo.Select("temperature").Limit(5).Find(&temps)
```

### 批量写入

```go
batch := []DeviceMetric{
    {Time: now, Temperature: 25.0, Pressure: 100.0, Humidity: 60.0},
    {Time: now + 60000, Temperature: 25.5, Pressure: 100.2, Humidity: 60.3},
}
repo.CreateInBatches(batch, 100) // 每批 100 条
```

### 原生 SQL 查询

```go
var results []DeviceMetric
sql := "SELECT temperature FROM root.factory.device01 WHERE time >= 1000 ORDER BY time DESC LIMIT 3"
repo.Raw(sql, &results)
```

## 项目结构

```
iotdborm/
├── repo.go          # 核心 Repository 实现
├── query.go         # 查询构建与结果映射
├── write.go         # 批量写入与时间序列创建
├── builder.go       # QueryBuilder 查询构建器
├── pool.go          # SessionPool 连接池
├── types.go         # 类型定义与映射
├── metadata.go      # 设备元数据管理
├── reflect.go       # 反射工具函数
├── mock/            # 内存 Mock 实现
└── examples/        # 使用示例
    ├── basic/       # 基础示例
    ├── query/       # 查询示例
    ├── metadata/    # 元数据管理示例
    └── mock/        # Mock 示例
```

## 运行示例

```bash
# 基础示例（需要 IoTDB 服务）
cd examples/basic
go run . -host 127.0.0.1 -port 6667 -user root -password root

# Mock 示例（无需 IoTDB）
cd examples/mock
go run .
```

## 许可证

MIT License

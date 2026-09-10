// mock 示例：无需真实IoTDB服务即可离线体验完整API
//
// 使用 iotdborm/mock 内存后端，API 与连接真实服务完全一致，
// 适合快速上手、编写单元测试、演示业务流程。
//
// 运行方式：
//
//	go run .
package main

import (
	"fmt"
	"time"

	"github.com/niehz/iotdb_repo"
	"github.com/niehz/iotdb_repo/mock"
)

// DeviceMetric 设备测点数据结构
type DeviceMetric struct {
	Time        int64   `iotdb:"time"`
	Temperature float64 `iotdb:"temperature"`
	Pressure    float64 `iotdb:"pressure"`
	Humidity    float64 `iotdb:"humidity"`
}

func main() {
	fmt.Println("=== IoTDB ORM 离线演示（内存后端）===")

	devicePath := "root.factory.workshop01.device01"

	// 1. 创建内存连接池（与真实连接池实现同一接口）
	pool := mock.NewPool()
	defer pool.Close()
	fmt.Println("1. 内存连接池创建成功")

	// 2. 创建Repository
	repo := iotdborm.NewRepo(pool, devicePath)
	fmt.Printf("2. Repository 创建成功，设备路径: %s\n", devicePath)

	// 3. 初始化时间序列
	metadata, err := iotdborm.NewDeviceMetadata(devicePath, DeviceMetric{})
	if err != nil {
		fmt.Printf("构建设备元数据失败: %v\n", err)
		return
	}
	if err := repo.CreateTimeseries(metadata); err != nil {
		fmt.Printf("创建时间序列失败: %v\n", err)
		return
	}
	fmt.Println("3. 时间序列创建成功")

	// 4. 批量写入
	now := time.Now().UnixMilli() / 60000 * 60000
	var batch []DeviceMetric
	for i := 0; i < 10; i++ {
		batch = append(batch, DeviceMetric{
			Time:        now + int64(i)*60000,
			Temperature: 25.0 + float64(i)*0.5,
			Pressure:    100.0 + float64(i)*0.2,
			Humidity:    60.0 + float64(i)*0.3,
		})
	}
	if err := repo.CreateInBatches(batch, 3); err != nil {
		fmt.Printf("批量写入失败: %v\n", err)
		return
	}
	fmt.Println("4. 批量写入成功（10条，每批3条）")

	// 5. 查询全部
	var all []DeviceMetric
	if err := repo.Find(&all); err != nil {
		fmt.Printf("查询失败: %v\n", err)
		return
	}
	fmt.Printf("5. 查询到 %d 条数据:\n", len(all))
	for i, m := range all {
		fmt.Printf("   %d: time=%s temp=%.2f pressure=%.2f humidity=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature, m.Pressure, m.Humidity)
	}

	// 6. 条件查询
	var hot []DeviceMetric
	err = repo.
		Where("temperature >= ?", 28.0).
		Order("time", true).
		Limit(3).
		Find(&hot)
	if err != nil {
		fmt.Printf("条件查询失败: %v\n", err)
		return
	}
	fmt.Printf("6. 温度>=28的最近 %d 条:\n", len(hot))
	for i, m := range hot {
		fmt.Printf("   %d: time=%s temp=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature)
	}

	// 7. QueryBuilder
	builder := iotdborm.NewQueryBuilder(repo)
	var filtered []DeviceMetric
	err = builder.
		Where("time >= ?", now+2*60000).
		Where("time <= ?", now+6*60000).
		Where("humidity < ?", 62.0).
		Select("temperature", "humidity").
		Order("time", false).
		Find(&filtered)
	if err != nil {
		fmt.Printf("QueryBuilder查询失败: %v\n", err)
		return
	}
	fmt.Printf("7. QueryBuilder组合条件查询返回 %d 条:\n", len(filtered))
	for i, m := range filtered {
		fmt.Printf("   %d: time=%s temp=%.2f humidity=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature, m.Humidity)
	}

	// 8. First 与 Raw
	var first DeviceMetric
	if err := repo.Order("time", true).First(&first); err != nil {
		fmt.Printf("First查询失败: %v\n", err)
		return
	}
	fmt.Printf("8. 最新一条: time=%s temp=%.2f\n",
		time.UnixMilli(first.Time).Format("15:04:05"), first.Temperature)

	var raw []DeviceMetric
	sql := fmt.Sprintf("SELECT temperature FROM %s WHERE time >= %d ORDER BY time DESC LIMIT 2",
		devicePath, now+5*60000)
	if err := repo.Raw(sql, &raw); err != nil {
		fmt.Printf("原生SQL查询失败: %v\n", err)
		return
	}
	fmt.Printf("9. 原生SQL查询返回 %d 条:\n", len(raw))
	for i, m := range raw {
		fmt.Printf("   %d: time=%s temp=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature)
	}

	fmt.Println("\n离线演示完成，连接真实IoTDB只需把 mock.NewPool() 换成 iotdborm.NewSessionPool(...)")
}

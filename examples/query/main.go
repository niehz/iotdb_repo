// query 示例：演示 QueryBuilder 复杂查询构建
//
// 运行方式（需要先启动 IoTDB 服务，默认 127.0.0.1:6667 root/root）：
//
//	go run . -host 127.0.0.1 -port 6667 -user root -password root
//
// 注意事项：
//   - Select 中不要写 time 列（IoTDB 1.3.1 服务端缺陷，显式查询 time 会断开连接），
//     time 列总是自动作为第一列返回，iotdborm 会自动过滤
//   - WHERE/ORDER BY 中使用 time 是安全的（本示例的 Order("time", ...) 不受影响）
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"iotdborm"
)

// DeviceMetric 设备测点数据结构，支持 gorm 与 iotdb 双标签
type DeviceMetric struct {
	Time        int64   `gorm:"column:ts;tag:time" iotdb:"time"`
	Temperature float64 `gorm:"column:temp;type:float64" iotdb:"temperature"`
	Pressure    float64 `gorm:"column:pressure;type:float64" iotdb:"pressure"`
	Humidity    float64 `gorm:"column:humidity;type:float64" iotdb:"humidity"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "IoTDB地址")
	port := flag.Int("port", 6667, "IoTDB端口")
	user := flag.String("user", "root", "用户名")
	password := flag.String("password", "root", "密码")
	flag.Parse()

	devicePath := "root.factory.workshop02.device02"

	// 准备数据
	pool, err := iotdborm.NewSessionPool(*host, *port, *user, *password, 5)
	if err != nil {
		log.Fatal("创建SessionPool失败: ", err)
	}
	defer pool.Close()

	repo := iotdborm.NewRepo(pool, devicePath)

	metadata, err := iotdborm.NewDeviceMetadata(devicePath, DeviceMetric{})
	if err != nil {
		log.Fatal("构建设备元数据失败: ", err)
	}
	if err := repo.CreateTimeseries(metadata); err != nil {
		fmt.Printf("创建时间序列失败（可能已存在）: %v\n", err)
	}

	now := time.Now().UnixMilli()
	var batch []DeviceMetric
	for i := 0; i < 20; i++ {
		batch = append(batch, DeviceMetric{
			Time:        now + int64(i)*60000,
			Temperature: 20.0 + float64(i%10), // 20.0 ~ 29.0 循环
			Pressure:    100.0 + float64(i%10)*0.5,
			Humidity:    60.0 + float64(i%10),
		})
	}
	if err := repo.CreateInBatches(batch, 10); err != nil {
		log.Fatal("批量写入失败: ", err)
	}
	fmt.Println("已写入20条测试数据")

	// 1. 基础链式查询：多个Where条件以AND组合
	builder := iotdborm.NewQueryBuilder(repo)
	var result []DeviceMetric
	err = builder.
		Where("time >= ?", now).
		Where("temperature < ?", 25.0).
		Where("humidity >= ?", 62.0).
		Select("temperature", "humidity").
		Order("time", false).
		Limit(10).
		Find(&result)
	if err != nil {
		log.Fatal("查询失败: ", err)
	}
	fmt.Printf("1. 多条件AND查询返回 %d 条:\n", len(result))
	for i, m := range result {
		fmt.Printf("   %d: time=%s temp=%.1f humidity=%.1f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature, m.Humidity)
	}

	// 2. OR条件：温度过高或过低
	var orResult []DeviceMetric
	builder = iotdborm.NewQueryBuilder(repo)
	err = builder.
		Where("temperature >= ?", 29.0).
		Or("temperature <= ?", 20.0).
		Order("time", false).
		Limit(10).
		Find(&orResult)
	if err != nil {
		log.Fatal("查询失败: ", err)
	}
	fmt.Printf("2. OR条件查询返回 %d 条:\n", len(orResult))
	for i, m := range orResult {
		fmt.Printf("   %d: time=%s temp=%.1f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature)
	}

	// 3. 分页查询：Offset + Limit
	var page2 []DeviceMetric
	builder = iotdborm.NewQueryBuilder(repo)
	err = builder.
		Order("time", false).
		Limit(5).
		Offset(5).
		Find(&page2)
	if err != nil {
		log.Fatal("查询失败: ", err)
	}
	fmt.Printf("3. 分页查询（第2页）返回 %d 条:\n", len(page2))
	for i, m := range page2 {
		fmt.Printf("   %d: time=%s temp=%.1f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature)
	}

	// 4. First：查询第一条满足条件的记录
	var first DeviceMetric
	builder = iotdborm.NewQueryBuilder(repo)
	err = builder.
		Where("temperature > ?", 27.0).
		Order("time", true).
		First(&first)
	if err != nil {
		log.Fatal("First查询失败: ", err)
	}
	fmt.Printf("4. 最新一条温度大于27的记录: time=%s temp=%.1f\n",
		time.UnixMilli(first.Time).Format("15:04:05"), first.Temperature)

	// 5. Build + FindWithCondition：先构建条件再执行
	var built []DeviceMetric
	builder = iotdborm.NewQueryBuilder(repo)
	condition := builder.
		Where("time >= ? AND time <= ?", now+5*60000, now+15*60000).
		Select("pressure").
		Order("time", false).
		Build()
	if err := repo.FindWithCondition(&built, condition); err != nil {
		log.Fatal("FindWithCondition失败: ", err)
	}
	fmt.Printf("5. Build条件后执行返回 %d 条:\n", len(built))
	for i, m := range built {
		fmt.Printf("   %d: time=%s pressure=%.1f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Pressure)
	}

	fmt.Println("\n查询示例完成")
}

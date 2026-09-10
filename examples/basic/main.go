// basic 示例：演示 iotdborm 的完整使用流程
//
// 运行方式（需要先启动 IoTDB 服务，默认 127.0.0.1:6667 root/root）：
//
//	go run . -host 127.0.0.1 -port 6667 -user root -password root
//
// 如果想先离线体验，请查看 mock 示例：go run ../mock
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/niehz/iotdb_repo"
)

// DeviceMetric 设备测点数据结构
// Time 字段是IoTDB的时间戳（int64毫秒），其余字段通过 iotdb 标签映射为测点名称
type DeviceMetric struct {
	Time        int64   `iotdb:"time"`
	Temperature float64 `iotdb:"temperature"`
	Pressure    float64 `iotdb:"pressure"`
	Humidity    float64 `iotdb:"humidity"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "IoTDB地址")
	port := flag.Int("port", 6667, "IoTDB端口")
	user := flag.String("user", "root", "用户名")
	password := flag.String("password", "root", "密码")
	flag.Parse()

	devicePath := "root.factory.workshop01.device01"

	// 1. 创建SessionPool（全局复用，不要频繁创建销毁）
	pool, err := iotdborm.NewSessionPool(*host, *port, *user, *password, 5)
	if err != nil {
		log.Fatal("创建SessionPool失败: ", err)
	}
	defer pool.Close()
	fmt.Println("1. SessionPool 创建成功")

	// 2. 创建Repository，绑定设备路径
	repo := iotdborm.NewRepo(pool, devicePath)
	fmt.Printf("2. Repository 创建成功，设备路径: %s\n", devicePath)

	// 3. 初始化时间序列（首次运行需要，已存在会报错可忽略）
	metadata, err := iotdborm.NewDeviceMetadata(devicePath, DeviceMetric{})
	if err != nil {
		log.Fatal("构建设备元数据失败: ", err)
	}
	if err := repo.CreateTimeseries(metadata); err != nil {
		fmt.Printf("3. 创建时间序列失败（可能已存在）: %v\n", err)
	} else {
		fmt.Println("3. 时间序列创建成功")
	}

	// 4. 单条写入
	now := time.Now().UnixMilli()
	if err := repo.Create(&DeviceMetric{
		Time:        now,
		Temperature: 26.5,
		Pressure:    101.2,
		Humidity:    65.0,
	}); err != nil {
		log.Fatal("单条写入失败: ", err)
	}
	fmt.Println("4. 单条写入成功")

	// 5. 批量写入（内部使用Tablet高性能写入）
	var batch []DeviceMetric
	for i := 0; i < 10; i++ {
		batch = append(batch, DeviceMetric{
			Time:        now + int64(i+1)*60000, // 每条间隔1分钟
			Temperature: 25.0 + float64(i)*0.5,
			Pressure:    100.0 + float64(i)*0.2,
			Humidity:    60.0 + float64(i)*0.3,
		})
	}
	if err := repo.CreateInBatches(batch, 5); err != nil {
		log.Fatal("批量写入失败: ", err)
	}
	fmt.Println("5. 批量写入成功（10条，每批5条）")

	// 6. 查询全部数据
	var all []DeviceMetric
	if err := repo.Find(&all); err != nil {
		log.Fatal("查询失败: ", err)
	}
	fmt.Printf("6. 查询到 %d 条数据:\n", len(all))
	for i, m := range all {
		fmt.Printf("   %d: time=%s temp=%.2f pressure=%.2f humidity=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature, m.Pressure, m.Humidity)
	}

	// 7. 条件查询：时间范围 + 倒序 + 分页
	var recent []DeviceMetric
	err = repo.
		Where("time >= ? AND time <= ?", now-10*60000, now+10*60000).
		Order("time", true).
		Limit(3).
		Offset(1).
		Find(&recent)
	if err != nil {
		log.Fatal("条件查询失败: ", err)
	}
	fmt.Printf("7. 条件查询（时间范围+倒序+分页）返回 %d 条:\n", len(recent))
	for i, m := range recent {
		fmt.Printf("   %d: time=%s temp=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature)
	}

	// 8. 指定字段查询（只查温度，time列由IoTDB自动返回）
	var temps []struct {
		Time        int64   `iotdb:"time"`
		Temperature float64 `iotdb:"temperature"`
	}
	if err := repo.Select("temperature").Limit(5).Find(&temps); err != nil {
		log.Fatal("指定字段查询失败: ", err)
	}
	fmt.Printf("8. 指定字段查询返回 %d 条温度数据:\n", len(temps))
	for i, m := range temps {
		fmt.Printf("   %d: time=%s temp=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature)
	}

	// 9. 查询第一条记录（GORM风格，直接传入结构体指针）
	var first DeviceMetric
	if err := repo.Order("time", true).First(&first); err != nil {
		log.Fatal("First查询失败: ", err)
	}
	fmt.Printf("9. 最新一条: time=%s temp=%.2f\n",
		time.UnixMilli(first.Time).Format("15:04:05"), first.Temperature)

	// 10. 原生SQL查询（注意：不要显式查询time列，IoTDB会自动返回）
	var rawData []DeviceMetric
	rawSQL := fmt.Sprintf(
		"SELECT temperature FROM %s WHERE time >= %d ORDER BY time DESC LIMIT 3",
		devicePath, now-10*60000)
	if err := repo.Raw(rawSQL, &rawData); err != nil {
		log.Fatal("原生SQL查询失败: ", err)
	}
	fmt.Printf("10. 原生SQL查询返回 %d 条:\n", len(rawData))
	for i, m := range rawData {
		fmt.Printf("    %d: time=%s temp=%.2f\n",
			i+1, time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature)
	}

	// 11. IoTDB不支持的能力演示
	if err := repo.Update(&DeviceMetric{Time: now, Temperature: 28.0}); err != nil {
		fmt.Printf("11. 更新操作（预期失败）: %v\n", err)
	}
	if err := repo.Delete("time < ?", now-86400000); err != nil {
		fmt.Printf("    删除操作（预期失败）: %v\n", err)
	}

	fmt.Println("\n所有操作完成，更复杂的查询请查看 query 示例，元数据管理请查看 metadata 示例")
}

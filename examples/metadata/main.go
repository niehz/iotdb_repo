// metadata 示例：演示设备元数据管理器，解决跨设备查询问题
//
// 树形模型下无法像TDengine超级表那样一条SQL跨设备查询，
// 通过元数据管理器维护 deviceId -> devicePath 映射与设备标签，
// 业务层按标签筛选设备后再查询各设备路径。
//
// 运行方式（需要先启动 IoTDB 服务，默认 127.0.0.1:6667 root/root）：
//
//	go run . -host 127.0.0.1 -port 6667 -user root -password root
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"iotdborm"
)

// DeviceInfo 设备注册信息（仅用于元数据管理）
// gorm:"tag:xxx" 用于按标签检索设备
type DeviceInfo struct {
	DeviceId string `iotdb:"device_path"`
	Region   string `gorm:"tag:region"`
	Status   bool   `gorm:"tag:status"`
}

// DeviceMetric 设备测点数据，DeviceId 通过 iotdb:"-" 排除出测点，仅用于路径解析
type DeviceMetric struct {
	Time        int64   `iotdb:"time"`
	Temperature float64 `iotdb:"temperature"`
	Pressure    float64 `iotdb:"pressure"`
	DeviceId    string  `iotdb:"-"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "IoTDB地址")
	port := flag.Int("port", 6667, "IoTDB端口")
	user := flag.String("user", "root", "用户名")
	password := flag.String("password", "root", "密码")
	flag.Parse()

	pool, err := iotdborm.NewSessionPool(*host, *port, *user, *password, 5)
	if err != nil {
		log.Fatal("创建SessionPool失败: ", err)
	}
	defer pool.Close()

	// 1. 注册设备到元数据管理器
	manager := iotdborm.NewMemoryDeviceMetadataManager()

	devices := []struct {
		info DeviceInfo
		path string
	}{
		{DeviceInfo{DeviceId: "device01", Region: "north", Status: true}, "root.factory.north.device01"},
		{DeviceInfo{DeviceId: "device02", Region: "north", Status: true}, "root.factory.north.device02"},
		{DeviceInfo{DeviceId: "device03", Region: "south", Status: false}, "root.factory.south.device03"},
	}
	for _, d := range devices {
		if err := manager.RegisterDevice(d.path, d.info); err != nil {
			log.Fatalf("注册设备 %s 失败: %v", d.info.DeviceId, err)
		}
		fmt.Printf("1. 注册设备: %s -> %s\n", d.info.DeviceId, d.path)
	}

	// 2. 按ID获取设备路径
	if path, err := manager.GetDevicePath("device02"); err == nil {
		fmt.Printf("2. device02 的路径: %s\n", path)
	}

	// 3. 按标签筛选设备（例如查询north区域的所有设备）
	deviceIds, err := manager.ListDevicesByTag("region", "north")
	if err != nil {
		log.Fatal("按标签查询失败: ", err)
	}
	fmt.Printf("3. north区域的设备: %v\n", deviceIds)

	// 4. 字段映射管理
	if err := manager.RegisterFieldMapping("device01", "TempAlias", "temperature"); err != nil {
		log.Fatal("注册字段映射失败: ", err)
	}
	if tag, err := manager.GetFieldMapping("device01", "TempAlias"); err == nil {
		fmt.Printf("4. device01.TempAlias 映射到测点: %s\n", tag)
	}

	// 5. 跨设备查询计划：先得到设备列表与路径，再逐个查询
	results, err := manager.QueryMultipleDevices(
		deviceIds,
		time.Now().Add(-time.Hour).UnixMilli(),
		time.Now().UnixMilli(),
		[]string{"temperature", "pressure"},
	)
	if err != nil {
		log.Fatal("跨设备查询失败: ", err)
	}
	fmt.Printf("5. 跨设备查询计划生成 %d 个目标:\n", len(results))
	for _, r := range results {
		fmt.Printf("   %s -> %s, 测点: %v\n", r.DeviceId, r.DevicePath, r.Measurements)
	}

	// 6. 带元数据管理的Repository：写入时自动把 deviceId 解析为设备路径
	now := time.Now().UnixMilli()
	for _, d := range devices {
		// 初始化时间序列
		repo := iotdborm.NewRepoWithMetadata(pool, "", manager)
		md, err := iotdborm.NewDeviceMetadata(d.path, DeviceMetric{})
		if err != nil {
			log.Fatal("构建设备元数据失败: ", err)
		}
		if err := repo.CreateTimeseries(md); err != nil {
			fmt.Printf("   创建时间序列失败（可能已存在）: %s: %v\n", d.path, err)
		}

		// 写入时无需关心路径，自动解析
		if err := repo.Create(&DeviceMetric{
			Time:        now,
			Temperature: 26.0,
			Pressure:    101.0,
			DeviceId:    d.info.DeviceId,
		}); err != nil {
			log.Fatalf("写入 %s 失败: %v", d.info.DeviceId, err)
		}
		fmt.Printf("6. 写入成功（自动解析路径）: %s -> %s\n", d.info.DeviceId, d.path)
	}

	// 7. 按解析后的路径查询各设备数据
	for _, d := range devices {
		repo := iotdborm.NewRepo(pool, d.path)
		var data []DeviceMetric
		if err := repo.Find(&data); err != nil {
			log.Printf("查询 %s 失败: %v", d.path, err)
			continue
		}
		fmt.Printf("7. %s 查询到 %d 条数据\n", d.info.DeviceId, len(data))
		for _, m := range data {
			fmt.Printf("   time=%s temp=%.1f pressure=%.1f\n",
				time.UnixMilli(m.Time).Format("15:04:05"), m.Temperature, m.Pressure)
		}
	}

	fmt.Println("\n元数据管理示例完成")
}

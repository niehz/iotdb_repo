// metadata 示例：演示约定路径元数据管理器，免手写设备路径
//
// 核心思路：路径模板 root.factory.{region}.{deviceId}
//   - 注册时只传设备信息，路径按模板自动生成（RegisterDeviceAuto）
//   - 标签就是路径段：ListDevicesByTag 直查 IoTDB（show timeseries 前缀扫描），
//     新设备无需注册即可被查询到
//   - 写入时未注册的设备也能按模板自动推导路径（零注册直写）
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

	"github.com/niehz/iotdb_repo"
)

// DeviceInfo 设备注册信息
// gorm:"tag:xxx" 字段的值填充路径模板中的 {xxx} 占位符
type DeviceInfo struct {
	DeviceId string `iotdb:"device_path"`
	Region   string `gorm:"tag:region"`
	Status   bool   `gorm:"tag:status"`
}

// DeviceMetric 设备测点数据，DeviceId/Region 用于路径解析，iotdb:"-" 排除出测点
type DeviceMetric struct {
	Time        int64   `iotdb:"time"`
	Temperature float64 `iotdb:"temperature"`
	Pressure    float64 `iotdb:"pressure"`
	DeviceId    string  `iotdb:"-"`
	Region      string  `gorm:"tag:region"`
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

	// 1. 创建约定路径管理器：路径 = 模板自动生成，标签 = 路径段
	manager, err := iotdborm.NewConventionPathManager("root.factory.{region}.{deviceId}", pool)
	if err != nil {
		log.Fatal("创建约定路径管理器失败: ", err)
	}
	fmt.Println("1. 约定路径管理器创建成功，模板: root.factory.{region}.{deviceId}")

	// 2. 注册设备：只传设备信息，路径自动生成
	devices := []DeviceInfo{
		{DeviceId: "device01", Region: "north", Status: true},
		{DeviceId: "device02", Region: "north", Status: true},
		{DeviceId: "device03", Region: "south", Status: false},
	}
	for _, d := range devices {
		if err := manager.RegisterDeviceAuto(d); err != nil {
			log.Fatalf("注册设备 %s 失败: %v", d.DeviceId, err)
		}
		fmt.Printf("2. 注册设备（路径自动生成）: %s -> %s\n", d.DeviceId, resolvePath(manager, d))
	}

	// 3. 按标签查询设备：直查IoTDB，无需维护注册表
	deviceIds, err := manager.ListDevicesByTag("region", "north")
	if err != nil {
		log.Fatal("按标签查询失败: ", err)
	}
	fmt.Printf("3. north区域设备（直查IoTDB）: %v\n", deviceIds)

	// 4. 设备路径列表（show timeseries 前缀扫描）
	paths, err := manager.ListDevicePaths("root.factory.**")
	if err != nil {
		log.Fatal("设备路径列表失败: ", err)
	}
	fmt.Printf("4. IoTDB中的设备路径（自动发现）: %v\n", paths)

	// 5. 一键同步存量设备到注册表
	n, err := manager.SyncDevices("root.factory.**")
	if err != nil {
		log.Fatal("同步设备失败: ", err)
	}
	fmt.Printf("5. 同步存量设备完成，新增 %d 个\n", n)

	// 6. 零注册直写：新设备无需注册，路径按模板自动推导
	now := time.Now().UnixMilli()
	allDevices := append(devices, DeviceInfo{DeviceId: "device04", Region: "north", Status: true})
	repo := iotdborm.NewRepoWithMetadata(pool, "", manager)
	for _, d := range allDevices {
		path := resolvePath(manager, d)
		md, err := iotdborm.NewDeviceMetadata(path, DeviceMetric{})
		if err != nil {
			log.Fatal("构建设备元数据失败: ", err)
		}
		if err := repo.CreateTimeseries(md); err != nil {
			fmt.Printf("   创建时间序列失败（可能已存在）: %s\n", path)
		}
		if err := repo.Create(&DeviceMetric{
			Time:        now,
			Temperature: 26.0,
			Pressure:    101.0,
			DeviceId:    d.DeviceId,
			Region:      d.Region,
		}); err != nil {
			log.Fatalf("写入 %s 失败: %v", d.DeviceId, err)
		}
	}
	fmt.Println("6. 零注册直写成功（device04 未注册也按模板落到了 root.factory.north.device04）")

	// 7. 直查新设备数据：north 区域现在包含 device04
	deviceIds, err = manager.ListDevicesByTag("region", "north")
	if err != nil {
		log.Fatal("按标签查询失败: ", err)
	}
	fmt.Printf("7. 写入后north区域设备（实时）: %v\n", deviceIds)

	for _, id := range deviceIds {
		path, err := manager.GetDevicePath(id)
		if err != nil {
			continue
		}
		r := iotdborm.NewRepo(pool, path)
		var data []DeviceMetric
		if err := r.Find(&data); err != nil {
			log.Printf("查询 %s 失败: %v", path, err)
			continue
		}
		fmt.Printf("   %s 查询到 %d 条数据\n", id, len(data))
	}

	// 8. 兼容能力：跨设备查询计划
	results, err := manager.QueryMultipleDevices(
		deviceIds,
		time.Now().Add(-time.Hour).UnixMilli(),
		time.Now().UnixMilli(),
		[]string{"temperature", "pressure"},
	)
	if err != nil {
		log.Fatal("跨设备查询失败: ", err)
	}
	fmt.Printf("8. 跨设备查询计划生成 %d 个目标:\n", len(results))
	for _, r := range results {
		fmt.Printf("   %s -> %s, 测点: %v\n", r.DeviceId, r.DevicePath, r.Measurements)
	}

	fmt.Println("\n提示：路径无稳定规律时，仍可用 iotdborm.NewMemoryDeviceMetadataManager 显式注册")
	fmt.Println("元数据管理示例完成")
}

func resolvePath(manager *iotdborm.ConventionPathManager, d DeviceInfo) string {
	if path, err := manager.GetDevicePath(d.DeviceId); err == nil {
		return path
	}
	path, err := manager.BuildDevicePath(d)
	if err != nil {
		panic(err)
	}
	return path
}

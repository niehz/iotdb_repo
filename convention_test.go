package iotdborm_test

import (
	"testing"
	"time"

	"github.com/niehz/iotdb_repo"
	"github.com/niehz/iotdb_repo/mock"
)

type deviceInfo struct {
	DeviceId string `iotdb:"device_path"`
	Region   string `gorm:"tag:region"`
	Status   bool   `gorm:"tag:status"`
}

func newConventionManager(t *testing.T, pool iotdborm.SessionPool) *iotdborm.ConventionPathManager {
	t.Helper()
	manager, err := iotdborm.NewConventionPathManager("root.factory.{region}.{deviceId}", pool)
	if err != nil {
		t.Fatalf("NewConventionPathManager failed: %v", err)
	}
	return manager
}

func TestNewConventionPathManagerValidation(t *testing.T) {
	tests := []struct {
		name     string
		template string
		wantErr  bool
	}{
		{"正常模板", "root.factory.{region}.{deviceId}", false},
		{"空模板", "", true},
		{"空段", "root.factory..{deviceId}", true},
		{"末段不是deviceId", "root.factory.{region}", true},
		{"无占位符", "root.factory.north", true},
		{"非法占位符", "root.factory.{region", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := iotdborm.NewConventionPathManager(test.template, nil)
			if test.wantErr && err == nil {
				t.Fatal("expected error but got none")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestConventionBuildDevicePath(t *testing.T) {
	manager := newConventionManager(t, nil)

	path, err := manager.BuildDevicePath(deviceInfo{DeviceId: "d01", Region: "north", Status: true})
	if err != nil {
		t.Fatalf("BuildDevicePath failed: %v", err)
	}
	if path != "root.factory.north.d01" {
		t.Errorf("expected root.factory.north.d01, got %s", path)
	}

	type noRegion struct {
		DeviceId string `iotdb:"device_path"`
	}
	if _, err := manager.BuildDevicePath(noRegion{DeviceId: "d01"}); err == nil {
		t.Error("expected error when region tag missing")
	}

	if _, err := manager.BuildDevicePath(struct{ Name string }{Name: "x"}); err == nil {
		t.Error("expected error when device ID missing")
	}
}

func TestConventionRegisterAndGet(t *testing.T) {
	manager := newConventionManager(t, nil)

	err := manager.RegisterDeviceAuto(deviceInfo{DeviceId: "d01", Region: "north", Status: true})
	if err != nil {
		t.Fatalf("RegisterDeviceAuto failed: %v", err)
	}

	path, err := manager.GetDevicePath("d01")
	if err != nil {
		t.Fatalf("GetDevicePath failed: %v", err)
	}
	if path != "root.factory.north.d01" {
		t.Errorf("expected root.factory.north.d01, got %s", path)
	}

	if _, err := manager.GetDevicePath("unknown"); err == nil {
		t.Error("expected error for unregistered device")
	}
}

func TestConventionListDevicesByTagFallback(t *testing.T) {
	// 无连接池：回退到注册表缓存
	manager := newConventionManager(t, nil)

	devices := []deviceInfo{
		{DeviceId: "d01", Region: "north", Status: true},
		{DeviceId: "d02", Region: "north", Status: true},
		{DeviceId: "d03", Region: "south", Status: false},
	}
	for _, d := range devices {
		if err := manager.RegisterDeviceAuto(d); err != nil {
			t.Fatalf("RegisterDeviceAuto failed: %v", err)
		}
	}

	ids, err := manager.ListDevicesByTag("region", "north")
	if err != nil {
		t.Fatalf("ListDevicesByTag failed: %v", err)
	}
	if len(ids) != 2 || ids[0] != "d01" || ids[1] != "d02" {
		t.Errorf("expected [d01 d02], got %v", ids)
	}
}

func TestConventionDiscoveryWithMockPool(t *testing.T) {
	pool := mock.NewPool()
	manager := newConventionManager(t, pool)

	// 直接在IoTDB(mock)中建序列并写数据，不经过管理器注册
	devices := []string{
		"root.factory.north.d01",
		"root.factory.north.d02",
		"root.factory.south.d03",
	}
	type metric struct {
		Time        int64   `iotdb:"time"`
		Temperature float64 `iotdb:"temperature"`
	}
	for _, path := range devices {
		repo := iotdborm.NewRepo(pool, path)
		md, err := iotdborm.NewDeviceMetadata(path, metric{})
		if err != nil {
			t.Fatalf("NewDeviceMetadata failed: %v", err)
		}
		if err := repo.CreateTimeseries(md); err != nil {
			t.Fatalf("CreateTimeseries failed: %v", err)
		}
		if err := repo.Create(&metric{Time: time.Now().UnixMilli(), Temperature: 26.0}); err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	// ListDevicePaths：show timeseries 前缀扫描
	paths, err := manager.ListDevicePaths("root.factory.**")
	if err != nil {
		t.Fatalf("ListDevicePaths failed: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected 3 device paths, got %v", paths)
	}

	// ListDevicesByTag：直查IoTDB，无需注册
	ids, err := manager.ListDevicesByTag("region", "north")
	if err != nil {
		t.Fatalf("ListDevicesByTag failed: %v", err)
	}
	if len(ids) != 2 || ids[0] != "d01" || ids[1] != "d02" {
		t.Errorf("expected [d01 d02], got %v", ids)
	}

	// 标签不是路径段时报错
	if _, err := manager.ListDevicesByTag("status", "true"); err == nil {
		t.Error("expected error for tag not in template")
	}

	// SyncDevices：一键同步
	n, err := manager.SyncDevices("root.factory.**")
	if err != nil {
		t.Fatalf("SyncDevices failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 new devices, got %d", n)
	}

	// 同步后 GetDevicePath 可用
	if path, err := manager.GetDevicePath("d03"); err != nil || path != "root.factory.south.d03" {
		t.Errorf("GetDevicePath mismatch: %q, err=%v", path, err)
	}

	// 二次同步无新增
	if n, err := manager.SyncDevices("root.factory.**"); err != nil || n != 0 {
		t.Errorf("expected 0 new devices, got %d, err=%v", n, err)
	}
}

func TestConventionZeroRegistrationWrite(t *testing.T) {
	pool := mock.NewPool()
	manager := newConventionManager(t, pool)

	// 未注册任何设备，直接写入：路径按模板自动推导
	type data struct {
		Time        int64   `iotdb:"time"`
		Temperature float64 `iotdb:"temperature"`
		DeviceId    string  `iotdb:"-"`
		Region      string  `gorm:"tag:region"`
	}
	repo := iotdborm.NewRepoWithMetadata(pool, "root.default.device", manager)
	if err := repo.Create(&data{
		Time:        time.Now().UnixMilli(),
		Temperature: 30.0,
		DeviceId:    "d09",
		Region:      "north",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	realRepo := iotdborm.NewRepo(pool, "root.factory.north.d09")
	var got []data
	if err := realRepo.Find(&got); err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(got) != 1 || got[0].Temperature != 30.0 {
		t.Errorf("unexpected data at convention path: %+v", got)
	}
}

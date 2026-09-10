package iotdborm_test

import (
	"strconv"
	"testing"
	"time"

	"iotdborm"
	"iotdborm/mock"
)

type metric struct {
	Time        int64   `iotdb:"time"`
	Temperature float64 `iotdb:"temperature"`
	Pressure    float64 `iotdb:"pressure"`
	Humidity    float64 `iotdb:"humidity"`
}

func newTestRepo(t *testing.T) (*iotdborm.IotDBRepo, int64) {
	t.Helper()
	pool := mock.NewPool()
	repo := iotdborm.NewRepo(pool, "root.factory.workshop01.device01")
	return repo, time.Now().UnixMilli() / 60000 * 60000
}

func writeBatch(t *testing.T, repo *iotdborm.IotDBRepo, base int64, n int) {
	t.Helper()
	data := make([]metric, 0, n)
	for i := 0; i < n; i++ {
		data = append(data, metric{
			Time:        base + int64(i)*60000,
			Temperature: 20 + float64(i),
			Pressure:    100 + float64(i)/10,
			Humidity:    60 + float64(i)/5,
		})
	}
	if err := repo.CreateInBatches(data, 3); err != nil {
		t.Fatalf("CreateInBatches failed: %v", err)
	}
}

func TestCreateAndFindRoundtrip(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 10)

	var got []metric
	if err := repo.Find(&got); err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(got))
	}
	for i, m := range got {
		if m.Time != base+int64(i)*60000 {
			t.Errorf("row %d time mismatch: %d", i, m.Time)
		}
		if m.Temperature != 20+float64(i) {
			t.Errorf("row %d temperature mismatch: %v", i, m.Temperature)
		}
		if m.Pressure != 100+float64(i)/10 {
			t.Errorf("row %d pressure mismatch: %v", i, m.Pressure)
		}
	}
}

func TestFindWithWhereOrderLimit(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 10)

	// 时间范围 + 倒序 + 限制条数
	var got []metric
	err := repo.
		Where("time >= ? AND time <= ?", base+2*60000, base+8*60000).
		Order("time", true).
		Limit(3).
		Find(&got)
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	if got[0].Time != base+8*60000 {
		t.Errorf("expected latest row first, got %d", got[0].Time)
	}
	if got[2].Time != base+6*60000 {
		t.Errorf("expected third latest row, got %d", got[2].Time)
	}
}

func TestFindWithMeasurementCondition(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 10)

	var got []metric
	err := repo.
		Where("temperature > ?", 25.0).
		Order("time", false).
		Find(&got)
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(got))
	}
	if got[0].Temperature != 26.0 {
		t.Errorf("expected 26.0, got %v", got[0].Temperature)
	}
}

func TestSelectSubset(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 5)

	type partial struct {
		Time        int64   `iotdb:"time"`
		Temperature float64 `iotdb:"temperature"`
	}
	var got []partial
	err := repo.Select("time", "temperature").Find(&got)
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(got))
	}
	if got[2].Temperature != 22.0 {
		t.Errorf("expected 22.0, got %v", got[2].Temperature)
	}
}

func TestFirst(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 5)

	var m metric
	if err := repo.First(&m); err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if m.Time != base {
		t.Errorf("expected first row time %d, got %d", base, m.Time)
	}
}

func TestRawQuery(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 10)

	var got []metric
	sql := "SELECT time, temperature FROM root.factory.workshop01.device01 WHERE time >= " +
		strconv.FormatInt(base+3*60000, 10) + " ORDER BY time DESC LIMIT 2"
	if err := repo.Raw(sql, &got); err != nil {
		t.Fatalf("Raw failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	if got[0].Time != base+9*60000 || got[1].Time != base+8*60000 {
		t.Errorf("unexpected raw rows: %d, %d", got[0].Time, got[1].Time)
	}
}

func TestCreateTimeseries(t *testing.T) {
	repo, base := newTestRepo(t)

	md, err := iotdborm.NewDeviceMetadata("root.factory.workshop01.device01", metric{})
	if err != nil {
		t.Fatalf("NewDeviceMetadata failed: %v", err)
	}
	if err := repo.CreateTimeseries(md); err != nil {
		t.Fatalf("CreateTimeseries failed: %v", err)
	}

	if err := repo.Create(&metric{Time: base, Temperature: 21.5}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	var got []metric
	if err := repo.Find(&got); err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(got) != 1 || got[0].Temperature != 21.5 {
		t.Errorf("unexpected data: %+v", got)
	}
}

func TestQueryBuilderFirst(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 10)

	var m metric
	builder := iotdborm.NewQueryBuilder(repo)
	err := builder.
		Where("temperature > ?", 24.0).
		Order("time", true).
		First(&m)
	if err != nil {
		t.Fatalf("QueryBuilder First failed: %v", err)
	}
	if m.Time != base+9*60000 || m.Temperature != 29.0 {
		t.Errorf("unexpected first row: %+v", m)
	}
}

func TestQueryBuilderFind(t *testing.T) {
	repo, base := newTestRepo(t)
	writeBatch(t, repo, base, 10)

	var got []metric
	builder := iotdborm.NewQueryBuilder(repo)
	err := builder.
		Where("time >= ?", base).
		Where("humidity < ?", 61.0).
		Select("time", "temperature", "humidity").
		Order("time", false).
		Limit(3).
		Find(&got)
	if err != nil {
		t.Fatalf("QueryBuilder Find failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	if got[0].Time != base || got[0].Humidity != 60.0 {
		t.Errorf("unexpected first row: %+v", got[0])
	}
}

func TestMetadataManagerPathResolution(t *testing.T) {
	pool := mock.NewPool()
	manager := iotdborm.NewMemoryDeviceMetadataManager()

	type DeviceInfo struct {
		DeviceId string `iotdb:"device_path"`
		Region   string `gorm:"tag:region"`
	}

	if err := manager.RegisterDevice("root.factory.north.device01", DeviceInfo{DeviceId: "d1", Region: "north"}); err != nil {
		t.Fatalf("RegisterDevice failed: %v", err)
	}

	type data struct {
		Time        int64   `iotdb:"time"`
		Temperature float64 `iotdb:"temperature"`
		DeviceId    string  `iotdb:"-"`
	}

	// 默认路径仓库，写入时自动解析到注册的设备路径
	repo := iotdborm.NewRepoWithMetadata(pool, "root.default.device", manager)
	if err := repo.Create(&data{Time: time.Now().UnixMilli(), Temperature: 30.0, DeviceId: "d1"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 从解析后的路径查询
	realRepo := iotdborm.NewRepo(pool, "root.factory.north.device01")
	var got []data
	if err := realRepo.Find(&got); err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(got) != 1 || got[0].Temperature != 30.0 {
		t.Errorf("unexpected data at resolved path: %+v", got)
	}
}

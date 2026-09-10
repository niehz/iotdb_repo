package iotdborm

import (
	"testing"
	"time"
)

type testMetric struct {
	Time        int64   `iotdb:"time"`
	Temperature float64 `iotdb:"temperature" gorm:"column:temp;type:float64"`
	Pressure    float64 `gorm:"column:pressure"`
	Humidity    float64
	DeviceId    string `gorm:"tag:device_id"`
	Status      bool   `iotdb:"-"`
	hidden      string
}

func TestParseStructMetadata(t *testing.T) {
	metadata, err := parseStructMetadata(&testMetric{})
	if err != nil {
		t.Fatalf("parse struct metadata failed: %v", err)
	}

	expected := map[string]string{
		"Temperature": "temperature",
		"Pressure":    "pressure",
		"Humidity":    "Humidity",
		"DeviceId":    "device_id",
	}
	if len(metadata.Fields) != len(expected) {
		t.Fatalf("expected %d fields, got %d", len(expected), len(metadata.Fields))
	}
	for _, f := range metadata.Fields {
		tag, ok := expected[f.Name]
		if !ok {
			t.Errorf("unexpected field %s", f.Name)
			continue
		}
		if f.Tag != tag {
			t.Errorf("field %s tag mismatch: expected %q, got %q", f.Name, tag, f.Tag)
		}
	}
}

func TestParseStructMetadataUnsupportedType(t *testing.T) {
	type bad struct {
		Time int64
		V    complex64 `iotdb:"v"`
	}
	if _, err := parseStructMetadata(&bad{}); err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestParseGormTag(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`gorm:"column:temp;type:float64"`, "temp"},
		{`gorm:"tag:device_id;type:string"`, "device_id"},
		{`gorm:"type:int64;primary_key"`, ""},
		{`column:pressure`, "pressure"},
		{`tag:device_path`, "device_path"},
		{"", ""},
	}
	for _, test := range tests {
		if got := parseGormTag(test.input); got != test.expected {
			t.Errorf("parseGormTag(%q) = %q, expected %q", test.input, got, test.expected)
		}
	}
}

func TestExtractDeviceId(t *testing.T) {
	type d1 struct {
		DeviceId string
		Name     string
	}
	type d2 struct {
		Id    string `iotdb:"device_path"`
		Brand string
	}
	type d3 struct {
		X string `gorm:"tag:device_id"`
	}
	type d4 struct {
		Name  string
		Value float64
	}

	tests := []struct {
		name     string
		data     interface{}
		expected string
		wantErr  bool
	}{
		{"DeviceId字段", d1{DeviceId: "dev-1"}, "dev-1", false},
		{"iotdb device_path标签", d2{Id: "dev-2"}, "dev-2", false},
		{"gorm tag:device_id标签", d3{X: "dev-3"}, "dev-3", false},
		{"无设备ID字段", d4{Name: "x"}, "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := extractDeviceId(test.data)
			if test.wantErr && err == nil {
				t.Fatal("expected error but got none")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.expected {
				t.Errorf("extractDeviceId() = %q, expected %q", got, test.expected)
			}
		})
	}
}

func TestConvertToSlice(t *testing.T) {
	single := testMetric{Time: 1}
	got, err := convertToSlice(single)
	if err != nil {
		t.Fatalf("convertToSlice failed: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 item, got %d", len(got))
	}

	slice := []testMetric{{Time: 1}, {Time: 2}}
	got, err = convertToSlice(slice)
	if err != nil {
		t.Fatalf("convertToSlice failed: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 items, got %d", len(got))
	}

	ptrSlice := &[]testMetric{{Time: 3}, {Time: 4}, {Time: 5}}
	got, err = convertToSlice(ptrSlice)
	if err != nil {
		t.Fatalf("convertToSlice failed: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 items, got %d", len(got))
	}

	if got, _ = convertToSlice(nil); len(got) != 0 {
		t.Errorf("expected 0 items for nil, got %d", len(got))
	}

	empty := []testMetric{}
	if got, _ = convertToSlice(empty); len(got) != 0 {
		t.Errorf("expected 0 items for empty slice, got %d", len(got))
	}
}

func TestFormatWhere(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	tests := []struct {
		query    string
		args     []interface{}
		expected string
	}{
		{"no placeholder", nil, "no placeholder"},
		{"time >= ?", []interface{}{1000}, "time >= 1000"},
		{"region = ?", []interface{}{"north"}, "region = 'north'"},
		{"a = ? AND b = ?", []interface{}{"x", 2.5}, "a = 'x' AND b = 2.5"},
		{"time >= ?", []interface{}{now}, "time >= 1700000000000"},
		{"flag = ?", []interface{}{true}, "flag = true"},
	}
	for _, test := range tests {
		if got := formatWhere(test.query, test.args); got != test.expected {
			t.Errorf("formatWhere(%q, %v) = %q, expected %q", test.query, test.args, got, test.expected)
		}
	}
}

func TestBuildQuerySQL(t *testing.T) {
	repo := NewRepo(nil, "root.factory.device01")
	metadata, err := parseStructMetadata(&testMetric{})
	if err != nil {
		t.Fatalf("parse struct metadata failed: %v", err)
	}

	tests := []struct {
		name      string
		condition *QueryCondition
		expected  string
	}{
		{
			name:      "默认查询",
			condition: &QueryCondition{},
			expected:  "SELECT temperature, pressure, Humidity, device_id FROM root.factory.device01",
		},
		{
			name: "条件+排序+分页",
			condition: &QueryCondition{
				WhereClause: "time >= ?",
				WhereArgs:   []interface{}{int64(1000)},
				OrderField:  "time",
				OrderDesc:   true,
				Limit:       10,
				Offset:      2,
			},
			expected: "SELECT temperature, pressure, Humidity, device_id FROM root.factory.device01 WHERE time >= 1000 ORDER BY time DESC LIMIT 10 OFFSET 2",
		},
		{
			name: "指定字段+时间范围（显式time被过滤）",
			condition: &QueryCondition{
				SelectFields: []string{"time", "temperature"},
				TimeRange:    &TimeRange{StartTime: 100, EndTime: 200},
			},
			expected: "SELECT temperature FROM root.factory.device01 WHERE time >= 100 AND time <= 200",
		},
		{
			name: "只选time字段回退为全字段",
			condition: &QueryCondition{
				SelectFields: []string{"time"},
			},
			expected: "SELECT temperature, pressure, Humidity, device_id FROM root.factory.device01",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := repo.buildQuerySQLWithCondition(metadata, test.condition)
			if err != nil {
				t.Fatalf("buildQuerySQLWithCondition failed: %v", err)
			}
			if got != test.expected {
				t.Errorf("SQL mismatch:\n got: %s\nwant: %s", got, test.expected)
			}
		})
	}
}

func TestQueryBuilderBuild(t *testing.T) {
	repo := NewRepo(nil, "root.test.device")
	builder := NewQueryBuilder(repo)

	condition := builder.
		Where("time >= ?", 1000).
		Where("temperature < ?", 30.0).
		Or("pressure > ?", 101.0).
		Select("time", "temperature").
		Order("time", false).
		Limit(100).
		Offset(1).
		Build()

	if condition.WhereClause != "time >= ? AND (temperature < ?) OR (pressure > ?)" {
		t.Errorf("where clause mismatch: %q", condition.WhereClause)
	}
	if len(condition.WhereArgs) != 3 {
		t.Errorf("expected 3 args, got %d", len(condition.WhereArgs))
	}
	if len(condition.SelectFields) != 2 {
		t.Errorf("expected 2 select fields, got %d", len(condition.SelectFields))
	}
	if condition.OrderField != "time" || condition.OrderDesc {
		t.Errorf("order mismatch: %s desc=%v", condition.OrderField, condition.OrderDesc)
	}
	if condition.Limit != 100 || condition.Offset != 1 {
		t.Errorf("limit/offset mismatch: %d/%d", condition.Limit, condition.Offset)
	}
}

func TestMemoryDeviceMetadataManager(t *testing.T) {
	manager := NewMemoryDeviceMetadataManager()

	type DeviceInfo struct {
		DeviceId string `iotdb:"device_path"`
		Region   string `gorm:"tag:region"`
		Status   bool   `gorm:"tag:status"`
	}

	if err := manager.RegisterDevice("root.factory.north.device01", DeviceInfo{DeviceId: "device01", Region: "north", Status: true}); err != nil {
		t.Fatalf("RegisterDevice failed: %v", err)
	}
	if err := manager.RegisterDevice("root.factory.south.device02", DeviceInfo{DeviceId: "device02", Region: "south", Status: true}); err != nil {
		t.Fatalf("RegisterDevice failed: %v", err)
	}

	if path, err := manager.GetDevicePath("device01"); err != nil || path != "root.factory.north.device01" {
		t.Errorf("GetDevicePath mismatch: %q, err=%v", path, err)
	}

	devices, err := manager.ListDevicesByTag("region", "north")
	if err != nil {
		t.Fatalf("ListDevicesByTag failed: %v", err)
	}
	if len(devices) != 1 || devices[0] != "device01" {
		t.Errorf("expected ['device01'], got %v", devices)
	}

	if err := manager.RegisterFieldMapping("device01", "Name", "device_name"); err != nil {
		t.Fatalf("RegisterFieldMapping failed: %v", err)
	}
	if tag, err := manager.GetFieldMapping("device01", "Name"); err != nil || tag != "device_name" {
		t.Errorf("GetFieldMapping mismatch: %q, err=%v", tag, err)
	}

	results, err := manager.QueryMultipleDevices([]string{"device01", "device02"}, 1000, 2000, []string{"temperature"})
	if err != nil {
		t.Fatalf("QueryMultipleDevices failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if results[0].DevicePath != "root.factory.north.device01" {
		t.Errorf("unexpected device path: %s", results[0].DevicePath)
	}
}

package iotdborm

import (
	"reflect"

	"github.com/apache/iotdb-client-go/client"
)

// TSDataTypeMapping Go类型到IoTDB数据类型的映射
var TSDataTypeMapping = map[reflect.Type]client.TSDataType{
	reflect.TypeOf(int64(0)):    client.INT64,
	reflect.TypeOf(int32(0)):    client.INT32,
	reflect.TypeOf(int16(0)):    client.INT32,
	reflect.TypeOf(int8(0)):     client.INT32,
	reflect.TypeOf(int(0)):      client.INT64,
	reflect.TypeOf(uint64(0)):   client.INT64,
	reflect.TypeOf(uint32(0)):   client.INT64,
	reflect.TypeOf(uint16(0)):   client.INT64,
	reflect.TypeOf(uint8(0)):    client.INT64,
	reflect.TypeOf(uint(0)):     client.INT64,
	reflect.TypeOf(float64(0)):  client.DOUBLE,
	reflect.TypeOf(float32(0)):  client.FLOAT,
	reflect.TypeOf(bool(false)): client.BOOLEAN,
	reflect.TypeOf(""):          client.TEXT,
}

// FieldInfo 结构体字段信息
type FieldInfo struct {
	Name     string            // Go结构体字段名
	Type     reflect.Type      // Go字段类型
	DataType client.TSDataType // IoTDB数据类型
	Tag      string            // 测点名称
}

// DeviceMetadata 设备元数据信息
type DeviceMetadata struct {
	DevicePath string
	Fields     []FieldInfo
}

// QueryCondition 查询条件
type QueryCondition struct {
	WhereClause  string
	WhereArgs    []interface{}
	SelectFields []string
	OrderField   string
	OrderDesc    bool
	Limit        int
	Offset       int
	DevicePath   string
	TimeRange    *TimeRange
}

// TimeRange 时间范围
type TimeRange struct {
	StartTime int64
	EndTime   int64
}

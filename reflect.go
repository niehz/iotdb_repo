package iotdborm

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/apache/iotdb-client-go/client"
)

// parseStructMetadata 解析结构体元数据，支持 iotdb / gorm 双标签
func parseStructMetadata(data interface{}) (*DeviceMetadata, error) {
	dataValue := reflect.ValueOf(data)
	if dataValue.Kind() == reflect.Ptr {
		dataValue = dataValue.Elem()
	}
	if dataValue.Kind() != reflect.Struct {
		return nil, errors.New("data must be a struct or pointer to struct")
	}

	metadata := &DeviceMetadata{}
	dataType := dataValue.Type()
	for i := 0; i < dataType.NumField(); i++ {
		field := dataType.Field(i)
		if field.PkgPath != "" {
			continue // 跳过未导出字段
		}

		// iotdb 标签优先，其次从 gorm 标签解析，最后使用字段名
		tag := field.Tag.Get("iotdb")
		if tag == "" {
			if g := field.Tag.Get("gorm"); g != "" {
				tag = parseGormTag(g)
			}
		}
		if tag == "" {
			tag = field.Name
		}
		if tag == "-" {
			continue
		}

		// 时间戳字段：iotdb:"time" 或未显式标记的 Time 字段
		isTimeField := tag == "time" || (field.Name == "Time" && (tag == "" || tag == "Time"))
		if isTimeField {
			continue
		}

		dataTypeName, ok := TSDataTypeMapping[field.Type]
		if !ok {
			switch field.Type.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				dataTypeName = client.INT64
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				dataTypeName = client.INT64
			case reflect.Float32:
				dataTypeName = client.FLOAT
			case reflect.Float64:
				dataTypeName = client.DOUBLE
			case reflect.String:
				dataTypeName = client.TEXT
			case reflect.Bool:
				dataTypeName = client.BOOLEAN
			default:
				return nil, fmt.Errorf("unsupported type %s for field %s", field.Type, field.Name)
			}
		}

		metadata.Fields = append(metadata.Fields, FieldInfo{
			Name:     field.Name,
			Type:     field.Type,
			DataType: dataTypeName,
			Tag:      tag,
		})
	}

	return metadata, nil
}

// parseGormTag 解析gorm标签，优先提取column，其次提取tag
func parseGormTag(gormTag string) string {
	gormTag = strings.TrimSpace(gormTag)
	if strings.HasPrefix(gormTag, "gorm:\"") {
		gormTag = strings.TrimSuffix(strings.TrimPrefix(gormTag, "gorm:\""), "\"")
	}
	if c := gormTagPart(gormTag, "column"); c != "" {
		return c
	}
	if t := gormTagPart(gormTag, "tag"); t != "" {
		return t
	}
	return ""
}

// gormTagPart 提取gorm标签中指定key的值，格式示例："column:temp;type:float64;tag:device_id"
func gormTagPart(gormTag, key string) string {
	for _, part := range strings.Split(gormTag, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(part, key+":"))
		}
	}
	return ""
}

// extractDeviceId 从结构体中提取设备ID
func extractDeviceId(data interface{}) (string, error) {
	dataValue := reflect.ValueOf(data)
	if dataValue.Kind() == reflect.Ptr {
		dataValue = dataValue.Elem()
	}
	if dataValue.Kind() != reflect.Struct {
		return "", errors.New("data must be a struct or pointer to struct")
	}

	if f := dataValue.FieldByName("DeviceId"); f.IsValid() && f.Kind() == reflect.String {
		return f.String(), nil
	}

	dataType := dataValue.Type()
	for i := 0; i < dataType.NumField(); i++ {
		field := dataType.Field(i)
		fieldValue := dataValue.Field(i)
		if fieldValue.Kind() != reflect.String {
			continue
		}
		if tag := field.Tag.Get("iotdb"); tag == "device_path" || tag == "device_id" {
			return fieldValue.String(), nil
		}
		if t := gormTagPart(field.Tag.Get("gorm"), "tag"); t == "device_id" {
			return fieldValue.String(), nil
		}
	}

	return "", fmt.Errorf("no device ID field found in struct")
}

// extractRows 从批量数据中提取时间戳列和值列，values[c][r] 表示第c列第r行的值
func extractRows(items []interface{}, fields []FieldInfo) ([]int64, [][]interface{}, error) {
	timestamps := make([]int64, len(items))
	values := make([][]interface{}, len(fields))
	for c := range fields {
		values[c] = make([]interface{}, len(items))
	}

	for r, item := range items {
		ts, err := extractTimestamp(item)
		if err != nil {
			return nil, nil, err
		}
		timestamps[r] = ts

		dataValue := reflect.ValueOf(item)
		if dataValue.Kind() == reflect.Ptr {
			dataValue = dataValue.Elem()
		}
		for c, f := range fields {
			fv := dataValue.FieldByName(f.Name)
			if !fv.IsValid() || !fv.CanInterface() {
				values[c][r] = nil
				continue
			}
			values[c][r] = normalizeValue(fv, f.DataType)
		}
	}
	return timestamps, values, nil
}

// extractTimestamp 提取时间戳，优先Time字段，否则使用当前时间
func extractTimestamp(item interface{}) (int64, error) {
	dataValue := reflect.ValueOf(item)
	if dataValue.Kind() == reflect.Ptr {
		dataValue = dataValue.Elem()
	}
	if dataValue.Kind() != reflect.Struct {
		return 0, errors.New("data must be a struct or pointer to struct")
	}
	if tf := dataValue.FieldByName("Time"); tf.IsValid() && tf.Kind() == reflect.Int64 {
		return tf.Int(), nil
	}
	return time.Now().UnixMilli(), nil
}

// normalizeValue 将字段值转换为客户端Tablet要求的具体类型
func normalizeValue(v reflect.Value, dataType client.TSDataType) interface{} {
	switch dataType {
	case client.INT32:
		return int32(v.Int())
	case client.INT64, client.TIMESTAMP:
		return v.Int()
	case client.FLOAT:
		return float32(v.Float())
	case client.DOUBLE:
		return v.Float()
	case client.BOOLEAN:
		return v.Bool()
	case client.TEXT, client.STRING:
		return v.String()
	}
	return v.Interface()
}

// convertToSlice 将单个结构体或切片统一转换为[]interface{}
func convertToSlice(data interface{}) ([]interface{}, error) {
	if data == nil {
		return []interface{}{}, nil
	}
	dataValue := reflect.ValueOf(data)
	switch dataValue.Kind() {
	case reflect.Slice:
		result := make([]interface{}, dataValue.Len())
		for i := 0; i < dataValue.Len(); i++ {
			result[i] = dataValue.Index(i).Interface()
		}
		return result, nil
	case reflect.Ptr:
		if dataValue.IsNil() {
			return []interface{}{}, nil
		}
		if dataValue.Elem().Kind() == reflect.Slice {
			elem := dataValue.Elem()
			result := make([]interface{}, elem.Len())
			for i := 0; i < elem.Len(); i++ {
				result[i] = elem.Index(i).Interface()
			}
			return result, nil
		}
		return []interface{}{data}, nil
	default:
		return []interface{}{data}, nil
	}
}

// formatWhere 将占位符替换为格式化后的值，生成可直接执行的SQL
func formatWhere(query string, args []interface{}) string {
	if len(args) == 0 {
		return query
	}
	for _, arg := range args {
		query = strings.Replace(query, "?", formatValue(arg), 1)
	}
	return query
}

// formatValue 将参数格式化为SQL字面量
func formatValue(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case time.Time:
		return strconv.FormatInt(t.UnixMilli(), 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

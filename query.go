package iotdborm

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// resultRow 一行查询结果
type resultRow struct {
	values      []interface{}
	indexByName map[string]int
}

// FindWithCondition 带条件的查询，反射填充目标切片
func (r *IotDBRepo) FindWithCondition(dest interface{}, condition *QueryCondition) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Ptr || destValue.Elem().Kind() != reflect.Slice {
		return errors.New("dest must be a pointer to slice")
	}

	elemType := destValue.Elem().Type().Elem()
	example := reflect.New(elemType).Interface()
	metadata, err := parseStructMetadata(example)
	if err != nil {
		return fmt.Errorf("parse struct metadata failed: %v", err)
	}

	sql, err := r.buildQuerySQLWithCondition(metadata, condition)
	if err != nil {
		return fmt.Errorf("build query SQL failed: %v", err)
	}

	rows, err := r.queryRows(sql)
	if err != nil {
		return err
	}

	resultSlice := reflect.MakeSlice(destValue.Elem().Type(), 0, len(rows))
	for _, row := range rows {
		elem := reflect.New(elemType).Elem()
		if err := fillStructFromRow(elem, row, metadata); err != nil {
			return fmt.Errorf("fill struct from row failed: %v", err)
		}
		resultSlice = reflect.Append(resultSlice, elem)
	}
	destValue.Elem().Set(resultSlice)
	return nil
}

// Raw 执行原生SQL查询。
// 注意：SQL中不要显式查询time列（IoTDB 1.3.1 服务端缺陷会导致连接断开），time列会自动返回
func (r *IotDBRepo) Raw(sql string, dest interface{}) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Ptr || destValue.Elem().Kind() != reflect.Slice {
		return errors.New("dest must be a pointer to slice")
	}

	elemType := destValue.Elem().Type().Elem()
	example := reflect.New(elemType).Interface()
	metadata, err := parseStructMetadata(example)
	if err != nil {
		return fmt.Errorf("parse struct metadata failed: %v", err)
	}

	rows, err := r.queryRows(sql)
	if err != nil {
		return err
	}

	resultSlice := reflect.MakeSlice(destValue.Elem().Type(), 0, len(rows))
	for _, row := range rows {
		elem := reflect.New(elemType).Elem()
		if err := fillStructFromRow(elem, row, metadata); err != nil {
			return fmt.Errorf("fill struct from row failed: %v", err)
		}
		resultSlice = reflect.Append(resultSlice, elem)
	}
	destValue.Elem().Set(resultSlice)
	return nil
}

// DefaultQueryTimeoutMs 默认查询超时（毫秒），可全局修改
var DefaultQueryTimeoutMs int64 = 60000

// queryRows 执行查询并读取全部行数据
func (r *IotDBRepo) queryRows(sql string) ([]*resultRow, error) {
	session, err := r.pool.GetSession()
	if err != nil {
		return nil, fmt.Errorf("get session failed: %v", err)
	}
	defer r.pool.PutBack(session)

	timeout := DefaultQueryTimeoutMs
	rs, err := session.Query(sql, &timeout)
	if err != nil {
		return nil, fmt.Errorf("execute query failed: %v, sql: %s", err, sql)
	}
	defer rs.Close()

	names := rs.ColumnNames()
	types := rs.ColumnTypes()
	indexByName := make(map[string]int, len(names))
	for i, name := range names {
		indexByName[name] = i
		indexByName[strings.ToLower(name)] = i
	}

	var rows []*resultRow
	for {
		next, err := rs.Next()
		if err != nil {
			return nil, fmt.Errorf("read row failed: %v, sql: %s", err, sql)
		}
		if !next {
			break
		}
		row := &resultRow{values: make([]interface{}, len(names)), indexByName: indexByName}
		for i := range names {
			v, err := valueByType(rs, int32(i), types[i])
			if err != nil {
				return nil, fmt.Errorf("read column failed: %v, sql: %s", err, sql)
			}
			row.values[i] = v
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// valueByType 根据列类型读取值
func valueByType(rs ResultSet, index int32, columnType string) (interface{}, error) {
	isNull, err := rs.IsNull(index)
	if err != nil {
		return nil, err
	}
	if isNull {
		return nil, nil
	}
	switch strings.ToUpper(columnType) {
	case "TIMESTAMP", "INT64":
		return rs.GetLong(index)
	case "INT32":
		return rs.GetInt(index)
	case "FLOAT":
		return rs.GetFloat(index)
	case "DOUBLE":
		return rs.GetDouble(index)
	case "BOOLEAN":
		return rs.GetBoolean(index)
	case "TEXT", "STRING", "BLOB", "DATE":
		return rs.GetString(index)
	default:
		return nil, nil
	}
}

// fillStructFromRow 从行数据填充结构体，优先按列名匹配，其次按位置
func fillStructFromRow(elem reflect.Value, row *resultRow, metadata *DeviceMetadata) error {
	set := make(map[string]bool, len(metadata.Fields)+1)

	// 时间戳字段：优先Time列，否则取第一列
	if tf := elem.FieldByName("Time"); tf.IsValid() && tf.CanSet() {
		if idx, ok := row.indexByName["time"]; ok && idx < len(row.values) && row.values[idx] != nil {
			setFieldValue(tf, row.values[idx])
			set["Time"] = true
		} else if len(row.values) > 0 && row.values[0] != nil {
			setFieldValue(tf, row.values[0])
			set["Time"] = true
		}
	}

	// 测点字段：优先按测点名称匹配（支持全路径后缀匹配）
	for _, f := range metadata.Fields {
		fv := elem.FieldByName(f.Name)
		if !fv.IsValid() || !fv.CanSet() {
			continue
		}
		idx, ok := findColumnIndex(row, f.Tag)
		if !ok {
			continue
		}
		if row.values[idx] == nil {
			continue
		}
		setFieldValue(fv, row.values[idx])
		set[f.Name] = true
	}

	// 位置回退：未命中的字段按列顺序填充
	position := 1
	for _, f := range metadata.Fields {
		if set[f.Name] {
			position++
			continue
		}
		if position >= len(row.values) {
			break
		}
		fv := elem.FieldByName(f.Name)
		if !fv.IsValid() || !fv.CanSet() || row.values[position] == nil {
			position++
			continue
		}
		setFieldValue(fv, row.values[position])
		position++
	}

	return nil
}

// findColumnIndex 按测点名称查找列，支持 "root.a.b.temperature" 后缀匹配
func findColumnIndex(row *resultRow, tag string) (int, bool) {
	if idx, ok := row.indexByName[tag]; ok {
		return idx, true
	}
	for name, idx := range row.indexByName {
		if strings.HasSuffix(name, "."+tag) {
			return idx, true
		}
	}
	return 0, false
}

// setFieldValue 设置字段值，带类型转换
func setFieldValue(field reflect.Value, value interface{}) {
	if value == nil {
		return
	}
	if !field.CanSet() {
		return
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(toInt64(value))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(uint64(toInt64(value)))
	case reflect.Float32, reflect.Float64:
		field.SetFloat(toFloat64(value))
	case reflect.Bool:
		field.SetBool(toBool(value))
	case reflect.String:
		field.SetString(toString(value))
	default:
		if field.Type() == reflect.TypeOf(time.Time{}) {
			switch t := value.(type) {
			case time.Time:
				field.Set(reflect.ValueOf(t))
			case int64:
				field.Set(reflect.ValueOf(time.UnixMilli(t)))
			}
		}
	}
}

func toInt64(v interface{}) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int32:
		return int64(t)
	case int16:
		return int64(t)
	case int8:
		return int64(t)
	case int:
		return int64(t)
	case uint64:
		return int64(t)
	case uint32:
		return int64(t)
	case uint16:
		return int64(t)
	case uint8:
		return int64(t)
	case uint:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		i, _ := strconv.ParseInt(t, 10, 64)
		return i
	case time.Time:
		return t.UnixMilli()
	}
	return 0
}

func toFloat64(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func toBool(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	case int64:
		return t != 0
	case int:
		return t != 0
	}
	return false
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// buildQuerySQLWithCondition 构建带条件的查询SQL
func (r *IotDBRepo) buildQuerySQLWithCondition(metadata *DeviceMetadata, condition *QueryCondition) (string, error) {
	var selectFields []string
	if len(condition.SelectFields) > 0 {
		selectFields = condition.SelectFields
	} else {
		for _, f := range metadata.Fields {
			selectFields = append(selectFields, f.Tag)
		}
	}

	// IoTDB总是隐式返回time列，显式查询time在部分服务端版本（如1.3.1）会导致连接被断开
	filtered := make([]string, 0, len(selectFields))
	for _, f := range selectFields {
		if strings.EqualFold(strings.TrimSpace(f), "time") {
			continue
		}
		filtered = append(filtered, f)
	}
	if len(filtered) == 0 {
		for _, f := range metadata.Fields {
			filtered = append(filtered, f.Tag)
		}
	}
	selectFields = filtered

	fromClause := condition.DevicePath
	if fromClause == "" {
		fromClause = r.devicePath
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(selectFields, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(fromClause)

	whereClause := formatWhere(condition.WhereClause, condition.WhereArgs)
	if tr := condition.TimeRange; tr != nil {
		extra := fmt.Sprintf("time >= %d AND time <= %d", tr.StartTime, tr.EndTime)
		if whereClause != "" {
			whereClause += " AND " + extra
		} else {
			whereClause = extra
		}
	}
	if whereClause != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(whereClause)
	}

	if condition.OrderField != "" {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(condition.OrderField)
		if condition.OrderDesc {
			sb.WriteString(" DESC")
		} else {
			sb.WriteString(" ASC")
		}
	}

	if condition.Limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT %d", condition.Limit))
	}
	if condition.Offset > 0 {
		sb.WriteString(fmt.Sprintf(" OFFSET %d", condition.Offset))
	}

	return sb.String(), nil
}

// resetQueryCondition 重置查询条件
func (r *IotDBRepo) resetQueryCondition() {
	r.queryCondition = QueryCondition{}
}

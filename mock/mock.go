// Package mock 提供内存版IoTDB后端，便于在没有真实IoTDB服务时运行示例和测试
package mock

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/apache/iotdb-client-go/client"

	"github.com/niehz/iotdb_repo"
)

// Point 单个数据点
type Point struct {
	Ts    int64
	Value interface{}
}

// Pool 内存连接池，实现 iotdborm.SessionPool
type Pool struct {
	mu     sync.RWMutex
	closed bool
	series map[string]client.TSDataType // 完整路径 -> 数据类型
	data   map[string][]Point           // 完整路径 -> 按时间排序的数据点
}

// NewPool 创建内存连接池
func NewPool() *Pool {
	return &Pool{
		series: make(map[string]client.TSDataType),
		data:   make(map[string][]Point),
	}
}

// GetSession 获取会话
func (p *Pool) GetSession() (iotdborm.Session, error) {
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("mock session pool has closed")
	}
	return &Session{pool: p}, nil
}

// PutBack 归还会话
func (p *Pool) PutBack(session iotdborm.Session) {}

// Close 关闭连接池
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
}

// Session 内存会话，实现 iotdborm.Session
type Session struct {
	pool *Pool
}

// CreateTimeseries 注册时间序列
func (s *Session) CreateTimeseries(path string, dataType client.TSDataType, encoding client.TSEncoding,
	compressor client.TSCompressionType, attributes map[string]string, tags map[string]string) error {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	s.pool.series[path] = dataType
	if _, ok := s.pool.data[path]; !ok {
		s.pool.data[path] = []Point{}
	}
	return nil
}

// InsertTablet 批量写入数据点
func (s *Session) InsertTablet(deviceId string, measurements []string, dataTypes []client.TSDataType,
	timestamps []int64, values [][]interface{}) error {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	for i, m := range measurements {
		full := deviceId + "." + m
		if _, ok := s.pool.series[full]; !ok {
			s.pool.series[full] = dataTypes[i]
		}
		for r, ts := range timestamps {
			s.pool.data[full] = append(s.pool.data[full], Point{Ts: ts, Value: values[i][r]})
		}
		sort.Slice(s.pool.data[full], func(a, b int) bool {
			return s.pool.data[full][a].Ts < s.pool.data[full][b].Ts
		})
	}
	return nil
}

// Query 执行查询，支持库生成的SQL子集
func (s *Session) Query(sql string, timeoutMs *int64) (iotdborm.ResultSet, error) {
	s.pool.mu.RLock()
	defer s.pool.mu.RUnlock()
	return s.pool.execute(sql)
}

// Execute 执行非查询语句，内存实现为空操作
func (s *Session) Execute(sql string) error { return nil }

// query 解析后的查询
type query struct {
	fields   []string
	device   string
	conds    []cond
	order    string
	orderAsc bool
	hasOrder bool
	hasLimit bool
	limit    int
	offset   int
}

type cond struct {
	field string
	op    string
	value interface{}
}

var (
	queryRe = regexp.MustCompile(`(?is)^\s*SELECT\s+(.+?)\s+FROM\s+(\S+)(?:\s+WHERE\s+(.*?))?(?:\s+ORDER\s+BY\s+(\S+)\s*(ASC|DESC)?)?(?:\s+LIMIT\s+(\d+))?(?:\s+OFFSET\s+(\d+))?\s*$`)
	condRe  = regexp.MustCompile(`^\s*(\S+)\s*(>=|<=|<>|!=|=|>|<)\s*(.+?)\s*$`)
)

// execute 执行查询
func (p *Pool) execute(sql string) (iotdborm.ResultSet, error) {
	q, err := parseQuery(sql)
	if err != nil {
		return nil, err
	}

	columns, types := p.resolveColumns(q)
	if len(columns) == 0 {
		return &ResultSet{}, nil
	}

	timestamps := p.candidateTimestamps(q, columns)

	rows := make([][]interface{}, 0)
	for _, ts := range timestamps {
		row := make([]interface{}, len(columns))
		row[0] = ts
		for ci := 1; ci < len(columns); ci++ {
			row[ci] = p.valueAt(columns[ci], ts)
		}
		if !passesConds(q.conds, row, columns) {
			continue
		}
		rows = append(rows, row)
	}

	// 排序
	if q.hasOrder && q.order != "" && !strings.EqualFold(q.order, "time") {
		ci := findMeasurementColumn(columns, q.order)
		if ci > 0 {
			sort.SliceStable(rows, func(a, b int) bool {
				cmp := compareValues(rows[a][ci], rows[b][ci])
				if q.orderAsc {
					return cmp < 0
				}
				return cmp > 0
			})
		}
	} else if q.hasOrder && strings.EqualFold(q.order, "time") && !q.orderAsc {
		sort.SliceStable(rows, func(a, b int) bool { return rows[a][0].(int64) > rows[b][0].(int64) })
	}

	// 分页
	if q.offset > 0 {
		if q.offset >= len(rows) {
			rows = rows[:0]
		} else {
			rows = rows[q.offset:]
		}
	}
	if q.hasLimit && q.limit < len(rows) {
		rows = rows[:q.limit]
	}

	return &ResultSet{columns: columns, types: types, rows: rows}, nil
}

// resolveColumns 解析SELECT列，返回列名（首列为Time，其余为完整路径）与列类型
func (p *Pool) resolveColumns(q *query) ([]string, []string) {
	var measurements []string
	for _, f := range q.fields {
		f = strings.TrimSpace(f)
		if strings.EqualFold(f, "time") {
			continue
		}
		if f == "*" {
			for _, m := range p.deviceMeasurements(q.device) {
				measurements = append(measurements, m)
			}
			continue
		}
		measurements = append(measurements, q.device+"."+f)
	}

	columns := []string{"Time"}
	types := []string{"TIMESTAMP"}
	for _, m := range measurements {
		columns = append(columns, m)
		types = append(types, dataTypeName(p.series[m]))
	}
	return columns, types
}

// candidateTimestamps 候选时间戳：所查询测点的并集
func (p *Pool) candidateTimestamps(q *query, columns []string) []int64 {
	set := make(map[int64]struct{})
	if len(columns) == 1 {
		for _, m := range p.deviceMeasurements(q.device) {
			for _, pt := range p.data[m] {
				set[pt.Ts] = struct{}{}
			}
		}
	} else {
		for _, m := range columns[1:] {
			for _, pt := range p.data[m] {
				set[pt.Ts] = struct{}{}
			}
		}
	}
	result := make([]int64, 0, len(set))
	for ts := range set {
		result = append(result, ts)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// deviceMeasurements 返回设备下所有测点完整路径（排序保证确定性）
func (p *Pool) deviceMeasurements(device string) []string {
	prefix := device + "."
	var result []string
	for path := range p.series {
		if strings.HasPrefix(path, prefix) {
			result = append(result, path)
		}
	}
	sort.Strings(result)
	return result
}

// valueAt 取某测点某时间点的值，不存在返回nil
func (p *Pool) valueAt(full string, ts int64) interface{} {
	points := p.data[full]
	i := sort.Search(len(points), func(i int) bool { return points[i].Ts >= ts })
	if i < len(points) && points[i].Ts == ts {
		return points[i].Value
	}
	return nil
}

// parseQuery 解析查询SQL
func parseQuery(sql string) (*query, error) {
	m := queryRe.FindStringSubmatch(sql)
	if m == nil {
		return nil, fmt.Errorf("unsupported query: %s", sql)
	}

	q := &query{orderAsc: true}
	for _, f := range strings.Split(m[1], ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			q.fields = append(q.fields, f)
		}
	}
	q.device = m[2]

	if m[3] != "" {
		for _, part := range regexp.MustCompile(`(?i)\s+AND\s+`).Split(m[3], -1) {
			cm := condRe.FindStringSubmatch(part)
			if cm == nil {
				return nil, fmt.Errorf("unsupported where condition: %s", part)
			}
			q.conds = append(q.conds, cond{field: cm[1], op: cm[2], value: parseValue(cm[3])})
		}
	}

	if m[4] != "" {
		q.hasOrder = true
		q.order = m[4]
		q.orderAsc = !strings.EqualFold(m[5], "DESC")
	}
	if m[6] != "" {
		q.hasLimit = true
		q.limit, _ = strconv.Atoi(m[6])
	}
	if m[7] != "" {
		q.offset, _ = strconv.Atoi(m[7])
	}
	return q, nil
}

// parseValue 解析条件值
func parseValue(s string) interface{} {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	if strings.Contains(s, ".") {
		f, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return f
		}
		return s
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return i
	}
	return s
}

// passesConds 判断行是否满足所有条件
func passesConds(conds []cond, row []interface{}, columns []string) bool {
	for _, c := range conds {
		if !evalCond(c, row, columns) {
			return false
		}
	}
	return true
}

func evalCond(c cond, row []interface{}, columns []string) bool {
	if strings.EqualFold(c.field, "time") {
		return compareOp(compareValues(row[0], c.value), c.op)
	}
	ci := findMeasurementColumn(columns, c.field)
	if ci < 0 || row[ci] == nil {
		return false
	}
	return compareOp(compareValues(row[ci], c.value), c.op)
}

// findMeasurementColumn 按测点名查找列，支持全路径后缀匹配
func findMeasurementColumn(columns []string, field string) int {
	for i, name := range columns {
		if i == 0 {
			continue
		}
		if name == field || strings.HasSuffix(name, "."+field) {
			return i
		}
	}
	return -1
}

func compareOp(cmp int, op string) bool {
	switch op {
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	case ">=":
		return cmp >= 0
	case "<=":
		return cmp <= 0
	case "=":
		return cmp == 0
	case "!=", "<>":
		return cmp != 0
	}
	return false
}

func compareValues(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			switch {
			case af < bf:
				return -1
			case af > bf:
				return 1
			default:
				return 0
			}
		}
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func toFloat(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case int32:
		return float64(t), true
	case int:
		return float64(t), true
	case float64:
		return t, true
	case float32:
		return float64(t), true
	}
	return 0, false
}

// dataTypeName 数据类型名称
func dataTypeName(t client.TSDataType) string {
	switch t {
	case client.BOOLEAN:
		return "BOOLEAN"
	case client.INT32:
		return "INT32"
	case client.INT64:
		return "INT64"
	case client.FLOAT:
		return "FLOAT"
	case client.DOUBLE:
		return "DOUBLE"
	case client.TEXT, client.STRING:
		return "TEXT"
	default:
		return "TEXT"
	}
}

// ResultSet 内存结果集，实现 iotdborm.ResultSet
type ResultSet struct {
	columns []string
	types   []string
	rows    [][]interface{}
	idx     int
}

// Next 移动到下一行
func (r *ResultSet) Next() (bool, error) {
	r.idx++
	return r.idx <= len(r.rows), nil
}

// ColumnNames 列名
func (r *ResultSet) ColumnNames() []string { return r.columns }

// ColumnTypes 列类型
func (r *ResultSet) ColumnTypes() []string { return r.types }

// IsNull 判断当前行指定列是否为空
func (r *ResultSet) IsNull(index int32) (bool, error) {
	row, err := r.current()
	if err != nil {
		return false, err
	}
	if int(index) < 0 || int(index) >= len(row) {
		return false, fmt.Errorf("column index out of range: %d", index)
	}
	return row[index] == nil, nil
}

// GetLong 读取int64值
func (r *ResultSet) GetLong(index int32) (int64, error) {
	v, err := r.value(index)
	if err != nil || v == nil {
		return 0, err
	}
	switch t := v.(type) {
	case int64:
		return t, nil
	case int32:
		return int64(t), nil
	case int:
		return int64(t), nil
	case float64:
		return int64(t), nil
	case float32:
		return int64(t), nil
	}
	return 0, nil
}

// GetInt 读取int32值
func (r *ResultSet) GetInt(index int32) (int32, error) {
	v, err := r.value(index)
	if err != nil || v == nil {
		return 0, err
	}
	switch t := v.(type) {
	case int32:
		return t, nil
	case int64:
		return int32(t), nil
	case int:
		return int32(t), nil
	case float64:
		return int32(t), nil
	case float32:
		return int32(t), nil
	}
	return 0, nil
}

// GetFloat 读取float32值
func (r *ResultSet) GetFloat(index int32) (float32, error) {
	v, err := r.value(index)
	if err != nil || v == nil {
		return 0, err
	}
	switch t := v.(type) {
	case float32:
		return t, nil
	case float64:
		return float32(t), nil
	case int64:
		return float32(t), nil
	}
	return 0, nil
}

// GetDouble 读取float64值
func (r *ResultSet) GetDouble(index int32) (float64, error) {
	v, err := r.value(index)
	if err != nil || v == nil {
		return 0, err
	}
	switch t := v.(type) {
	case float64:
		return t, nil
	case float32:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case int32:
		return float64(t), nil
	}
	return 0, nil
}

// GetBoolean 读取bool值
func (r *ResultSet) GetBoolean(index int32) (bool, error) {
	v, err := r.value(index)
	if err != nil || v == nil {
		return false, err
	}
	if b, ok := v.(bool); ok {
		return b, nil
	}
	return false, nil
}

// GetString 读取string值
func (r *ResultSet) GetString(index int32) (string, error) {
	v, err := r.value(index)
	if err != nil || v == nil {
		return "", err
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case []byte:
		return string(t), nil
	default:
		return fmt.Sprint(t), nil
	}
}

// Close 关闭结果集
func (r *ResultSet) Close() error { return nil }

// current 当前行
func (r *ResultSet) current() ([]interface{}, error) {
	if r.idx <= 0 || r.idx > len(r.rows) {
		return nil, fmt.Errorf("no current row")
	}
	return r.rows[r.idx-1], nil
}

// value 当前行指定列的值
func (r *ResultSet) value(index int32) (interface{}, error) {
	row, err := r.current()
	if err != nil {
		return nil, err
	}
	if int(index) < 0 || int(index) >= len(row) {
		return nil, fmt.Errorf("column index out of range: %d", index)
	}
	return row[index], nil
}

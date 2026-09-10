package iotdborm

import (
	"fmt"

	"github.com/apache/iotdb-client-go/client"
)

// Session 会话抽象，屏蔽底层客户端差异，便于测试与替换实现
type Session interface {
	CreateTimeseries(path string, dataType client.TSDataType, encoding client.TSEncoding,
		compressor client.TSCompressionType, attributes map[string]string, tags map[string]string) error
	InsertTablet(deviceId string, measurements []string, dataTypes []client.TSDataType,
		timestamps []int64, values [][]interface{}) error
	Query(sql string, timeoutMs *int64) (ResultSet, error)
	Execute(sql string) error
}

// SessionPool 连接池抽象
type SessionPool interface {
	GetSession() (Session, error)
	PutBack(session Session)
	Close()
}

// ResultSet 查询结果集抽象
type ResultSet interface {
	Next() (bool, error)
	ColumnNames() []string
	ColumnTypes() []string
	IsNull(index int32) (bool, error)
	GetLong(index int32) (int64, error)
	GetInt(index int32) (int32, error)
	GetFloat(index int32) (float32, error)
	GetDouble(index int32) (float64, error)
	GetBoolean(index int32) (bool, error)
	GetString(index int32) (string, error)
	Close() error
}

// defaultPool 基于官方客户端的连接池实现
type defaultPool struct {
	inner *client.SessionPool
}

func (p *defaultPool) GetSession() (Session, error) {
	s, err := p.inner.GetSession()
	if err != nil {
		return nil, err
	}
	return &clientSession{inner: &s}, nil
}

func (p *defaultPool) PutBack(session Session) {
	if s, ok := session.(*clientSession); ok {
		p.inner.PutBack(*s.inner)
	}
}

func (p *defaultPool) Close() {
	p.inner.Close()
}

// clientSession 官方客户端的会话适配
type clientSession struct {
	inner *client.Session
}

func (s *clientSession) CreateTimeseries(path string, dataType client.TSDataType, encoding client.TSEncoding,
	compressor client.TSCompressionType, attributes map[string]string, tags map[string]string) error {
	return s.inner.CreateTimeseries(path, dataType, encoding, compressor, attributes, tags)
}

func (s *clientSession) InsertTablet(deviceId string, measurements []string, dataTypes []client.TSDataType,
	timestamps []int64, values [][]interface{}) error {
	schemas := make([]*client.MeasurementSchema, len(measurements))
	for i, m := range measurements {
		schemas[i] = &client.MeasurementSchema{Measurement: m, DataType: dataTypes[i]}
	}
	tablet, err := client.NewTablet(deviceId, schemas, len(timestamps))
	if err != nil {
		return err
	}
	for i, ts := range timestamps {
		tablet.SetTimestamp(ts, i)
		for c := range measurements {
			if err := tablet.SetValueAt(values[c][i], c, i); err != nil {
				return err
			}
		}
		tablet.RowSize++
	}
	return s.inner.InsertTablet(tablet, false)
}

func (s *clientSession) Query(sql string, timeoutMs *int64) (ResultSet, error) {
	ds, err := s.inner.ExecuteQueryStatement(sql, timeoutMs)
	if err != nil {
		return nil, err
	}
	return &clientResultSet{inner: ds}, nil
}

func (s *clientSession) Execute(sql string) error {
	return s.inner.ExecuteNonQueryStatement(sql)
}

// clientResultSet 官方客户端结果集适配（对外统一0基索引，官方客户端为1基索引）
type clientResultSet struct {
	inner *client.SessionDataSet
}

func (r *clientResultSet) Next() (bool, error) { return r.inner.Next() }
func (r *clientResultSet) ColumnNames() []string {
	return r.inner.GetColumnNames()
}
func (r *clientResultSet) ColumnTypes() []string { return r.inner.GetColumnTypes() }
func (r *clientResultSet) IsNull(index int32) (bool, error) {
	return r.inner.IsNullByIndex(index + 1)
}
func (r *clientResultSet) GetLong(index int32) (int64, error) {
	return r.inner.GetLongByIndex(index + 1)
}
func (r *clientResultSet) GetInt(index int32) (int32, error) {
	return r.inner.GetIntByIndex(index + 1)
}
func (r *clientResultSet) GetFloat(index int32) (float32, error) {
	return r.inner.GetFloatByIndex(index + 1)
}
func (r *clientResultSet) GetDouble(index int32) (float64, error) {
	return r.inner.GetDoubleByIndex(index + 1)
}
func (r *clientResultSet) GetBoolean(index int32) (bool, error) {
	return r.inner.GetBooleanByIndex(index + 1)
}
func (r *clientResultSet) GetString(index int32) (string, error) {
	return r.inner.GetStringByIndex(index + 1)
}
func (r *clientResultSet) Close() error { return r.inner.Close() }

// NewSessionPool 创建SessionPool
func NewSessionPool(host string, port int, username, password string, poolSize int) (SessionPool, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 6667
	}
	if poolSize <= 0 {
		poolSize = 5
	}
	conf := &client.PoolConfig{
		Host:     host,
		Port:     fmt.Sprintf("%d", port),
		UserName: username,
		Password: password,
	}
	sp := client.NewSessionPool(conf, poolSize, 60000, 60000, false)
	return &defaultPool{inner: &sp}, nil
}

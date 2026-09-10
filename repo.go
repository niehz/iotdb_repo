package iotdborm

import (
	"errors"
	"fmt"
	"reflect"
)

// MetricRepo 统一仓储接口，兼容GORM常用方法签名
type MetricRepo interface {
	// 链式查询条件
	Where(query string, args ...interface{}) MetricRepo
	Or(query string, args ...interface{}) MetricRepo
	Select(fields ...string) MetricRepo
	Order(field string, desc bool) MetricRepo
	Limit(limit int) MetricRepo
	Offset(offset int) MetricRepo

	// 查询方法
	Find(dest interface{}) error
	First(dest interface{}) error
	Raw(sql string, dest interface{}) error

	// 写入方法
	Create(data interface{}) error
	CreateInBatches(data interface{}, batchSize int) error
	Update(data interface{}) error
	Delete(query string, args ...interface{}) error

	// 事务支持（IoTDB不支持，保留接口保持兼容）
	Begin() MetricRepo
	Commit() error
	Rollback() error

	// 工具方法
	GetModel() interface{}
	Table() string
}

// IotDBRepo 仓储实现，绑定一个设备路径（如 root.factory.workshop01.device01）
type IotDBRepo struct {
	pool            SessionPool
	devicePath      string
	metadataManager DeviceMetadataManager
	queryCondition  QueryCondition
	isTransaction   bool
}

// NewRepo 创建仓储，绑定设备路径
func NewRepo(pool SessionPool, devicePath string) *IotDBRepo {
	return &IotDBRepo{
		pool:       pool,
		devicePath: devicePath,
	}
}

// NewRepoWithMetadata 创建带元数据管理的仓储，写入时可自动解析设备路径
func NewRepoWithMetadata(pool SessionPool, devicePath string, metadataManager DeviceMetadataManager) *IotDBRepo {
	return &IotDBRepo{
		pool:            pool,
		devicePath:      devicePath,
		metadataManager: metadataManager,
	}
}

// Where 添加查询条件，支持 ? 占位符
func (r *IotDBRepo) Where(query string, args ...interface{}) MetricRepo {
	r.queryCondition.WhereClause = query
	r.queryCondition.WhereArgs = args
	return r
}

// Or 追加OR查询条件
func (r *IotDBRepo) Or(query string, args ...interface{}) MetricRepo {
	if r.queryCondition.WhereClause != "" {
		r.queryCondition.WhereClause = fmt.Sprintf("(%s) OR (%s)", r.queryCondition.WhereClause, query)
	} else {
		r.queryCondition.WhereClause = query
	}
	r.queryCondition.WhereArgs = append(r.queryCondition.WhereArgs, args...)
	return r
}

// Select 指定查询字段。
// 注意：无需显式选择time列，IoTDB总是自动返回time作为第一列；
// 显式选择time在部分服务端版本（如1.3.1）会触发服务端缺陷（连接断开），库内部会自动过滤
func (r *IotDBRepo) Select(fields ...string) MetricRepo {
	r.queryCondition.SelectFields = fields
	return r
}

// Order 设置排序
func (r *IotDBRepo) Order(field string, desc bool) MetricRepo {
	r.queryCondition.OrderField = field
	r.queryCondition.OrderDesc = desc
	return r
}

// Limit 设置返回条数上限
func (r *IotDBRepo) Limit(limit int) MetricRepo {
	r.queryCondition.Limit = limit
	return r
}

// Offset 设置偏移量
func (r *IotDBRepo) Offset(offset int) MetricRepo {
	r.queryCondition.Offset = offset
	return r
}

// Find 查询多条
func (r *IotDBRepo) Find(dest interface{}) error {
	defer r.resetQueryCondition()
	return r.FindWithCondition(dest, &r.queryCondition)
}

// First 查询第一条，支持传入结构体指针或切片指针
func (r *IotDBRepo) First(dest interface{}) error {
	defer r.resetQueryCondition()
	originalLimit := r.queryCondition.Limit
	r.queryCondition.Limit = 1
	defer func() { r.queryCondition.Limit = originalLimit }()
	return r.firstWithCondition(dest, &r.queryCondition)
}

// firstWithCondition 带条件查询第一条，dest可为结构体指针或切片指针
func (r *IotDBRepo) firstWithCondition(dest interface{}, condition *QueryCondition) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() == reflect.Ptr && destValue.Elem().Kind() == reflect.Struct {
		slice := reflect.New(reflect.SliceOf(destValue.Elem().Type()))
		if err := r.FindWithCondition(slice.Interface(), condition); err != nil {
			return err
		}
		if slice.Elem().Len() == 0 {
			return errors.New("record not found")
		}
		destValue.Elem().Set(slice.Elem().Index(0))
		return nil
	}
	return r.FindWithCondition(dest, condition)
}

// Create 写入单条
func (r *IotDBRepo) Create(data interface{}) error {
	return r.CreateInBatches([]interface{}{data}, 1)
}

// Update IoTDB不支持更新操作
func (r *IotDBRepo) Update(data interface{}) error {
	return fmt.Errorf("IoTDB does not support update operations, only insert operations are supported")
}

// Delete IoTDB不支持删除操作
func (r *IotDBRepo) Delete(query string, args ...interface{}) error {
	return fmt.Errorf("IoTDB does not support delete operations, use time range filtering instead")
}

// Begin 事务开始（IoTDB不支持，占位实现）
func (r *IotDBRepo) Begin() MetricRepo {
	r.isTransaction = true
	return r
}

// Commit 事务提交（IoTDB不支持，占位实现）
func (r *IotDBRepo) Commit() error {
	r.isTransaction = false
	return nil
}

// Rollback 事务回滚（IoTDB不支持，占位实现）
func (r *IotDBRepo) Rollback() error {
	r.isTransaction = false
	return nil
}

// GetModel IoTDB无需预定义模型
func (r *IotDBRepo) GetModel() interface{} {
	return nil
}

// Table 返回设备路径
func (r *IotDBRepo) Table() string {
	return r.devicePath
}

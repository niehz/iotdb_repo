package iotdborm

import (
	"errors"
	"fmt"
	"strings"
)

// QueryBuilder 查询构建器，用于构建复杂查询条件
type QueryBuilder struct {
	repo         MetricRepo
	conditions   []string
	args         []interface{}
	selectFields []string
	orderField   string
	orderDesc    bool
	limit        int
	offset       int
}

// NewQueryBuilder 创建查询构建器
func NewQueryBuilder(repo MetricRepo) *QueryBuilder {
	return &QueryBuilder{
		repo:       repo,
		conditions: make([]string, 0),
		args:       make([]interface{}, 0),
	}
}

// Where 添加查询条件，支持 ? 占位符
func (qb *QueryBuilder) Where(query string, args ...interface{}) *QueryBuilder {
	qb.conditions = append(qb.conditions, query)
	qb.args = append(qb.args, args...)
	return qb
}

// Or 在上一个条件上追加OR
func (qb *QueryBuilder) Or(query string, args ...interface{}) *QueryBuilder {
	if len(qb.conditions) == 0 {
		return qb.Where(query, args...)
	}
	last := qb.conditions[len(qb.conditions)-1]
	qb.conditions[len(qb.conditions)-1] = fmt.Sprintf("(%s) OR (%s)", last, query)
	qb.args = append(qb.args, args...)
	return qb
}

// Select 指定查询字段
func (qb *QueryBuilder) Select(fields ...string) *QueryBuilder {
	qb.selectFields = fields
	return qb
}

// Order 设置排序
func (qb *QueryBuilder) Order(field string, desc bool) *QueryBuilder {
	qb.orderField = field
	qb.orderDesc = desc
	return qb
}

// Limit 设置返回条数上限
func (qb *QueryBuilder) Limit(limit int) *QueryBuilder {
	qb.limit = limit
	return qb
}

// Offset 设置偏移量
func (qb *QueryBuilder) Offset(offset int) *QueryBuilder {
	qb.offset = offset
	return qb
}

// Build 构建查询条件
func (qb *QueryBuilder) Build() *QueryCondition {
	whereClause := ""
	if len(qb.conditions) > 0 {
		whereClause = strings.Join(qb.conditions, " AND ")
	}
	return &QueryCondition{
		WhereClause:  whereClause,
		WhereArgs:    qb.args,
		SelectFields: qb.selectFields,
		OrderField:   qb.orderField,
		OrderDesc:    qb.orderDesc,
		Limit:        qb.limit,
		Offset:       qb.offset,
	}
}

// Find 执行查询
func (qb *QueryBuilder) Find(dest interface{}) error {
	condition := qb.Build()
	r, ok := qb.repo.(*IotDBRepo)
	if !ok {
		return errors.New("repo does not support query building")
	}
	return r.FindWithCondition(dest, condition)
}

// First 查询第一条，支持传入结构体指针或切片指针
func (qb *QueryBuilder) First(dest interface{}) error {
	condition := qb.Build()
	condition.Limit = 1
	r, ok := qb.repo.(*IotDBRepo)
	if !ok {
		return errors.New("repo does not support query building")
	}
	return r.firstWithCondition(dest, condition)
}

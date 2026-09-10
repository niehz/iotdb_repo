package iotdborm

import (
	"errors"
	"fmt"

	"github.com/apache/iotdb-client-go/client"
)

// CreateInBatches 批量写入，内部使用Tablet高性能写入
func (r *IotDBRepo) CreateInBatches(data interface{}, batchSize int) error {
	items, err := convertToSlice(data)
	if err != nil {
		return fmt.Errorf("convert data to slice failed: %v", err)
	}
	if len(items) == 0 {
		return nil
	}

	metadata, err := parseStructMetadata(items[0])
	if err != nil {
		return fmt.Errorf("parse struct metadata failed: %v", err)
	}
	if len(metadata.Fields) == 0 {
		return errors.New("no measurement fields found in struct")
	}

	devicePath := r.resolveDevicePath(items[0])
	metadata.DevicePath = devicePath

	tags := make([]string, len(metadata.Fields))
	dataTypes := make([]client.TSDataType, len(metadata.Fields))
	for i, f := range metadata.Fields {
		tags[i] = f.Tag
		dataTypes[i] = f.DataType
	}

	if batchSize <= 0 {
		batchSize = 1000
	}
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		if err := r.insertBatch(devicePath, tags, dataTypes, items[start:end], metadata.Fields); err != nil {
			return err
		}
	}
	return nil
}

// insertBatch 写入一批数据
func (r *IotDBRepo) insertBatch(devicePath string, tags []string, dataTypes []client.TSDataType,
	items []interface{}, fields []FieldInfo) error {
	timestamps, values, err := extractRows(items, fields)
	if err != nil {
		return fmt.Errorf("extract row values failed: %v", err)
	}

	session, err := r.pool.GetSession()
	if err != nil {
		return fmt.Errorf("get session failed: %v", err)
	}
	defer r.pool.PutBack(session)

	if err := session.InsertTablet(devicePath, tags, dataTypes, timestamps, values); err != nil {
		return fmt.Errorf("insert tablet failed: %v", err)
	}
	return nil
}

// CreateTimeseries 批量创建时间序列（用于初始化）
func (r *IotDBRepo) CreateTimeseries(metadata *DeviceMetadata) error {
	if metadata == nil {
		return errors.New("metadata is nil")
	}
	if len(metadata.Fields) == 0 {
		return errors.New("no fields in metadata")
	}

	devicePath := metadata.DevicePath
	if devicePath == "" {
		devicePath = r.devicePath
	}

	session, err := r.pool.GetSession()
	if err != nil {
		return fmt.Errorf("get session failed: %v", err)
	}
	defer r.pool.PutBack(session)

	for _, f := range metadata.Fields {
		path := devicePath + "." + f.Tag
		if err := session.CreateTimeseries(path, f.DataType, client.PLAIN, client.SNAPPY, nil, nil); err != nil {
			return fmt.Errorf("create timeseries %s failed: %v", path, err)
		}
	}
	return nil
}

// NewDeviceMetadata 从示例结构体构建设备元数据，简化CreateTimeseries调用
func NewDeviceMetadata(devicePath string, example interface{}) (*DeviceMetadata, error) {
	metadata, err := parseStructMetadata(example)
	if err != nil {
		return nil, err
	}
	metadata.DevicePath = devicePath
	return metadata, nil
}

// resolveDevicePath 优先通过元数据管理器解析设备路径，否则使用仓库默认路径；
// 约定路径管理器支持零注册：未注册时按模板自动推导路径
func (r *IotDBRepo) resolveDevicePath(data interface{}) string {
	if r.metadataManager == nil {
		return r.devicePath
	}
	deviceId, err := extractDeviceId(data)
	if err != nil {
		return r.devicePath
	}
	if path, err := r.metadataManager.GetDevicePath(deviceId); err == nil && path != "" {
		return path
	}
	if cm, ok := r.metadataManager.(*ConventionPathManager); ok {
		if path, err := cm.BuildDevicePath(data); err == nil {
			return path
		}
	}
	return r.devicePath
}

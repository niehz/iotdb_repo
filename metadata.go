package iotdborm

import (
	"fmt"
	"reflect"
	"sync"
)

// DeviceMetadataManager 设备元数据管理器
type DeviceMetadataManager interface {
	// 设备管理
	RegisterDevice(devicePath string, deviceInfo interface{}) error
	GetDevicePath(deviceId string) (string, error)
	ListDevicesByTag(tag string, value string) ([]string, error)

	// 字段映射管理
	RegisterFieldMapping(deviceId string, fieldName string, iotdbTag string) error
	GetFieldMapping(deviceId string, fieldName string) (string, error)

	// 批量查询支持
	QueryMultipleDevices(deviceIds []string, startTime, endTime int64, measurements []string) ([]MultiDeviceQueryResult, error)
}

// MultiDeviceQueryResult 跨设备查询结果描述
type MultiDeviceQueryResult struct {
	DeviceId     string
	DevicePath   string
	StartTime    int64
	EndTime      int64
	Measurements []string
}

// MemoryDeviceMetadataManager 内存设备元数据管理器
type MemoryDeviceMetadataManager struct {
	mu      sync.RWMutex
	devices map[string]string            // deviceId -> devicePath
	fields  map[string]map[string]string // deviceId -> fieldName -> iotdbTag
	tags    map[string]map[string]string // deviceId -> tagName -> tagValue
}

// NewMemoryDeviceMetadataManager 创建内存设备元数据管理器
func NewMemoryDeviceMetadataManager() *MemoryDeviceMetadataManager {
	return &MemoryDeviceMetadataManager{
		devices: make(map[string]string),
		fields:  make(map[string]map[string]string),
		tags:    make(map[string]map[string]string),
	}
}

// RegisterDevice 注册设备，从deviceInfo提取设备ID、字段映射和标签
func (m *MemoryDeviceMetadataManager) RegisterDevice(devicePath string, deviceInfo interface{}) error {
	deviceId, err := extractDeviceId(deviceInfo)
	if err != nil {
		return fmt.Errorf("extract device ID failed: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.devices[deviceId] = devicePath

	dataValue := reflect.ValueOf(deviceInfo)
	if dataValue.Kind() == reflect.Ptr {
		dataValue = dataValue.Elem()
	}
	if dataValue.Kind() != reflect.Struct {
		return nil
	}

	if _, ok := m.fields[deviceId]; !ok {
		m.fields[deviceId] = make(map[string]string)
	}
	if _, ok := m.tags[deviceId]; !ok {
		m.tags[deviceId] = make(map[string]string)
	}

	dataType := dataValue.Type()
	for i := 0; i < dataType.NumField(); i++ {
		field := dataType.Field(i)
		fieldValue := dataValue.Field(i)

		if tag := field.Tag.Get("iotdb"); tag != "" && tag != "time" {
			m.fields[deviceId][field.Name] = tag
		}
		if t := gormTagPart(field.Tag.Get("gorm"), "tag"); t != "" {
			if fieldValue.IsValid() && fieldValue.CanInterface() {
				m.tags[deviceId][t] = fmt.Sprintf("%v", fieldValue.Interface())
			}
		}
	}

	return nil
}

// GetDevicePath 根据设备ID获取设备路径
func (m *MemoryDeviceMetadataManager) GetDevicePath(deviceId string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	devicePath, ok := m.devices[deviceId]
	if !ok {
		return "", fmt.Errorf("device %s not found", deviceId)
	}
	return devicePath, nil
}

// ListDevicesByTag 按标签查询设备列表
func (m *MemoryDeviceMetadataManager) ListDevicesByTag(tag string, value string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for deviceId, tags := range m.tags {
		if tags[tag] == value {
			result = append(result, deviceId)
		}
	}
	return result, nil
}

// RegisterFieldMapping 注册字段映射
func (m *MemoryDeviceMetadataManager) RegisterFieldMapping(deviceId string, fieldName string, iotdbTag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.devices[deviceId]; !ok {
		return fmt.Errorf("device %s not registered", deviceId)
	}
	if _, ok := m.fields[deviceId]; !ok {
		m.fields[deviceId] = make(map[string]string)
	}
	m.fields[deviceId][fieldName] = iotdbTag
	return nil
}

// GetFieldMapping 获取字段映射
func (m *MemoryDeviceMetadataManager) GetFieldMapping(deviceId string, fieldName string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	fields, ok := m.fields[deviceId]
	if !ok {
		return "", fmt.Errorf("device %s not registered", deviceId)
	}
	iotdbTag, ok := fields[fieldName]
	if !ok {
		return "", fmt.Errorf("field %s not found for device %s", fieldName, deviceId)
	}
	return iotdbTag, nil
}

// QueryMultipleDevices 查询多个设备的元数据信息，实际数据查询需基于返回路径另行执行
func (m *MemoryDeviceMetadataManager) QueryMultipleDevices(deviceIds []string, startTime, endTime int64, measurements []string) ([]MultiDeviceQueryResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]MultiDeviceQueryResult, 0, len(deviceIds))
	for _, deviceId := range deviceIds {
		devicePath, ok := m.devices[deviceId]
		if !ok {
			continue
		}
		results = append(results, MultiDeviceQueryResult{
			DeviceId:     deviceId,
			DevicePath:   devicePath,
			StartTime:    startTime,
			EndTime:      endTime,
			Measurements: measurements,
		})
	}
	return results, nil
}

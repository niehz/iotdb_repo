package iotdborm

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ConventionDeviceMetadataManager 约定路径元数据管理器扩展接口
type ConventionDeviceMetadataManager interface {
	DeviceMetadataManager

	// RegisterDeviceAuto 按路径模板自动生成路径并注册，无需手写设备路径
	RegisterDeviceAuto(deviceInfo interface{}) error
	// BuildDevicePath 按模板与设备信息构建路径，不注册
	BuildDevicePath(deviceInfo interface{}) (string, error)
	// ListDevicePaths 通过 show timeseries 前缀扫描获取IoTDB中的设备路径列表
	ListDevicePaths(prefix string) ([]string, error)
	// SyncDevices 从IoTDB同步设备列表到注册表，返回新增设备数
	SyncDevices(prefix string) (int, error)
}

// pathSegment 模板片段：literal 为字面量段，ph 为占位符名（空表示字面量）
type pathSegment struct {
	literal string
	ph      string
}

var placeholderRe = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// ConventionPathManager 基于路径模板约定的设备元数据管理器
//
// 模板示例：root.factory.{region}.{deviceId}
//   - {deviceId} 占位符：由 extractDeviceId 填充（DeviceId字段 / iotdb:"device_path" / gorm:"tag:device_id"）
//   - 其他占位符：由设备信息结构体中 gorm:"tag:<name>" 字段的值填充
//
// 标签即路径段：ListDevicesByTag 通过 show timeseries 前缀扫描直接查询IoTDB，
// 新设备无需注册即可被查询到；注册表仅作为缓存。
type ConventionPathManager struct {
	mu       sync.RWMutex
	pool     SessionPool
	template string
	segments []pathSegment
	devices  map[string]string            // deviceId -> devicePath（缓存）
	tags     map[string]map[string]string // deviceId -> tagName -> tagValue（缓存）
	fields   map[string]map[string]string // deviceId -> fieldName -> iotdbTag
}

// NewConventionPathManager 创建约定路径管理器
func NewConventionPathManager(template string, pool SessionPool) (*ConventionPathManager, error) {
	m := &ConventionPathManager{
		pool:    pool,
		devices: make(map[string]string),
		tags:    make(map[string]map[string]string),
		fields:  make(map[string]map[string]string),
	}
	if err := m.setTemplate(template); err != nil {
		return nil, err
	}
	return m, nil
}

// setTemplate 解析并校验路径模板，要求最后一个占位符是 deviceId
func (m *ConventionPathManager) setTemplate(template string) error {
	template = strings.TrimSpace(template)
	if template == "" {
		return errors.New("path template is empty")
	}

	rawSegs := strings.Split(template, ".")
	segments := make([]pathSegment, 0, len(rawSegs))
	for _, raw := range rawSegs {
		if raw == "" {
			return fmt.Errorf("invalid path template %q: empty segment", template)
		}
		if match := placeholderRe.FindStringSubmatch(raw); match != nil {
			segments = append(segments, pathSegment{ph: match[1]})
		} else {
			if strings.ContainsAny(raw, "{}") {
				return fmt.Errorf("invalid path template %q: bad placeholder in segment %q", template, raw)
			}
			segments = append(segments, pathSegment{literal: raw})
		}
	}

	last := segments[len(segments)-1]
	if last.ph == "" || !isDeviceIdPlaceholder(last.ph) {
		return fmt.Errorf("invalid path template %q: last segment must be the deviceId placeholder like {deviceId}", template)
	}

	m.template = template
	m.segments = segments
	return nil
}

func isDeviceIdPlaceholder(name string) bool {
	switch strings.ToLower(name) {
	case "deviceid", "device_id":
		return true
	}
	return false
}

// BuildDevicePath 按模板与设备信息构建设备路径
func (m *ConventionPathManager) BuildDevicePath(deviceInfo interface{}) (string, error) {
	values := extractTagValues(deviceInfo)
	deviceId, err := extractDeviceId(deviceInfo)
	if err != nil {
		return "", err
	}
	values["deviceId"] = deviceId
	values["device_id"] = deviceId

	var sb strings.Builder
	for i, seg := range m.segments {
		if i > 0 {
			sb.WriteString(".")
		}
		if seg.ph == "" {
			sb.WriteString(seg.literal)
			continue
		}
		v, ok := values[seg.ph]
		if !ok || v == "" {
			return "", fmt.Errorf("cannot fill placeholder {%s}: device info has no field with gorm:\"tag:%s\"", seg.ph, seg.ph)
		}
		if strings.ContainsAny(v, ".{}") {
			return "", fmt.Errorf("value %q for placeholder {%s} contains invalid characters", v, seg.ph)
		}
		sb.WriteString(v)
	}
	return sb.String(), nil
}

// RegisterDeviceAuto 按模板自动生成路径并注册
func (m *ConventionPathManager) RegisterDeviceAuto(deviceInfo interface{}) error {
	devicePath, err := m.BuildDevicePath(deviceInfo)
	if err != nil {
		return err
	}
	return m.RegisterDevice(devicePath, deviceInfo)
}

// RegisterDevice 注册设备（显式路径覆盖约定，标签从路径段与设备信息反解）
func (m *ConventionPathManager) RegisterDevice(devicePath string, deviceInfo interface{}) error {
	deviceId, err := extractDeviceId(deviceInfo)
	if err != nil {
		return fmt.Errorf("extract device ID failed: %v", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.devices[deviceId] = devicePath

	if _, ok := m.tags[deviceId]; !ok {
		m.tags[deviceId] = make(map[string]string)
	}
	for tagName, tagValue := range parsePathTags(m.segments, devicePath) {
		m.tags[deviceId][tagName] = tagValue
	}
	for tagName, tagValue := range extractTagValues(deviceInfo) {
		m.tags[deviceId][tagName] = tagValue
	}

	if _, ok := m.fields[deviceId]; !ok {
		m.fields[deviceId] = make(map[string]string)
	}
	dataValue := reflect.ValueOf(deviceInfo)
	if dataValue.Kind() == reflect.Ptr {
		dataValue = dataValue.Elem()
	}
	if dataValue.Kind() == reflect.Struct {
		dataType := dataValue.Type()
		for i := 0; i < dataType.NumField(); i++ {
			field := dataType.Field(i)
			if tag := field.Tag.Get("iotdb"); tag != "" && tag != "time" {
				m.fields[deviceId][field.Name] = tag
			}
		}
	}

	return nil
}

// GetDevicePath 根据设备ID获取设备路径（查注册表缓存）
func (m *ConventionPathManager) GetDevicePath(deviceId string) (string, error) {
	m.mu.RLock()
	devicePath, ok := m.devices[deviceId]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("device %s not registered, try SyncDevices to load devices from IoTDB", deviceId)
	}
	return devicePath, nil
}

// ListDevicesByTag 按标签（路径段）查询设备列表。
// 配置了连接池时直查IoTDB（show timeseries 前缀扫描，实时结果，无需注册）；
// 无连接池时回退到注册表缓存
func (m *ConventionPathManager) ListDevicesByTag(tag string, value string) ([]string, error) {
	if m.pool != nil {
		prefix, err := m.tagWildcard(tag, value)
		if err != nil {
			return nil, err
		}
		paths, err := m.ListDevicePaths(prefix)
		if err != nil {
			return nil, err
		}
		idSet := make(map[string]struct{}, len(paths))
		for _, p := range paths {
			idSet[lastSegment(p)] = struct{}{}
		}
		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for deviceId, tags := range m.tags {
		if tags[tag] == value {
			result = append(result, deviceId)
		}
	}
	sort.Strings(result)
	return result, nil
}

// ListDevicePaths 通过 show timeseries 前缀扫描获取设备路径列表。
// prefix 需以 .** 结尾表示子树扫描（如 root.factory.**）
func (m *ConventionPathManager) ListDevicePaths(prefix string) ([]string, error) {
	if m.pool == nil {
		return nil, errors.New("no session pool configured for device discovery")
	}
	session, err := m.pool.GetSession()
	if err != nil {
		return nil, fmt.Errorf("get session failed: %v", err)
	}
	defer m.pool.PutBack(session)

	timeout := DefaultQueryTimeoutMs
	rs, err := session.Query("show timeseries "+prefix, &timeout)
	if err != nil {
		return nil, fmt.Errorf("show timeseries failed: %v", err)
	}
	defer rs.Close()

	pathCol := 0
	for i, name := range rs.ColumnNames() {
		if strings.EqualFold(name, "timeseries") {
			pathCol = i
			break
		}
	}

	deviceSet := make(map[string]struct{})
	for {
		next, err := rs.Next()
		if err != nil {
			return nil, fmt.Errorf("read show timeseries row failed: %v", err)
		}
		if !next {
			break
		}
		fullPath, err := rs.GetString(int32(pathCol))
		if err != nil || fullPath == "" {
			continue
		}
		deviceSet[trimLastSegment(fullPath)] = struct{}{}
	}

	result := make([]string, 0, len(deviceSet))
	for p := range deviceSet {
		result = append(result, p)
	}
	sort.Strings(result)
	return result, nil
}

// SyncDevices 从IoTDB同步设备列表到注册表，返回新增设备数
func (m *ConventionPathManager) SyncDevices(prefix string) (int, error) {
	paths, err := m.ListDevicePaths(prefix)
	if err != nil {
		return 0, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, p := range paths {
		deviceId := lastSegment(p)
		if _, ok := m.devices[deviceId]; !ok {
			count++
		}
		m.devices[deviceId] = p
		if _, ok := m.tags[deviceId]; !ok {
			m.tags[deviceId] = make(map[string]string)
		}
		for tagName, tagValue := range parsePathTags(m.segments, p) {
			m.tags[deviceId][tagName] = tagValue
		}
	}
	return count, nil
}

// RegisterFieldMapping 注册字段映射
func (m *ConventionPathManager) RegisterFieldMapping(deviceId string, fieldName string, iotdbTag string) error {
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
func (m *ConventionPathManager) GetFieldMapping(deviceId string, fieldName string) (string, error) {
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
func (m *ConventionPathManager) QueryMultipleDevices(deviceIds []string, startTime, endTime int64, measurements []string) ([]MultiDeviceQueryResult, error) {
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

// tagWildcard 按标签值生成通配前缀，其余占位符以 ** 代替
func (m *ConventionPathManager) tagWildcard(tag string, value string) (string, error) {
	if value == "" || strings.ContainsAny(value, ".{}") {
		return "", fmt.Errorf("invalid tag value %q", value)
	}
	found := false
	var sb strings.Builder
	for i, seg := range m.segments {
		if i > 0 {
			sb.WriteString(".")
		}
		switch {
		case seg.ph == "":
			sb.WriteString(seg.literal)
		case seg.ph == tag:
			sb.WriteString(value)
			found = true
		default:
			sb.WriteString("**")
		}
	}
	if !found {
		return "", fmt.Errorf("tag %q is not a path segment in template %q", tag, m.template)
	}
	return sb.String(), nil
}

// extractTagValues 提取结构体中 gorm:"tag:<name>" 字段的值
func extractTagValues(data interface{}) map[string]string {
	values := make(map[string]string)
	dataValue := reflect.ValueOf(data)
	if dataValue.Kind() == reflect.Ptr {
		if dataValue.IsNil() {
			return values
		}
		dataValue = dataValue.Elem()
	}
	if dataValue.Kind() != reflect.Struct {
		return values
	}
	dataType := dataValue.Type()
	for i := 0; i < dataType.NumField(); i++ {
		field := dataType.Field(i)
		tagName := gormTagPart(field.Tag.Get("gorm"), "tag")
		if tagName == "" {
			continue
		}
		fieldValue := dataValue.Field(i)
		if fieldValue.IsValid() && fieldValue.CanInterface() {
			values[tagName] = fmt.Sprintf("%v", fieldValue.Interface())
		}
	}
	return values
}

// parsePathTags 从设备路径按模板反解标签（标签=路径段）
func parsePathTags(segments []pathSegment, devicePath string) map[string]string {
	tags := make(map[string]string)
	parts := strings.Split(devicePath, ".")
	for i, seg := range segments {
		if seg.ph == "" || isDeviceIdPlaceholder(seg.ph) {
			continue
		}
		if i < len(parts) {
			tags[seg.ph] = parts[i]
		}
	}
	return tags
}

// trimLastSegment 去掉路径最后一段（测点名）
func trimLastSegment(path string) string {
	if i := strings.LastIndex(path, "."); i > 0 {
		return path[:i]
	}
	return path
}

// lastSegment 取路径最后一段
func lastSegment(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

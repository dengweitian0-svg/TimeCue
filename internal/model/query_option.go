package model

// Option 保存日程查询的可选条件；只有本包的选项函数可以修改它。
type Option struct {
	id             *int
	name           *string
	offset         *int
	limit          *int
	scheduleType   *ScheduleType
	includeDeleted bool
}

// QueryOption 为日程查询设置一个可选条件。
type QueryOption func(*Option)

// ResolveQueryOptions 依次应用选项，后面的同类选项覆盖前面的值。
func ResolveQueryOptions(options ...QueryOption) Option {
	var query Option
	for _, apply := range options {
		if apply != nil {
			apply(&query)
		}
	}
	return query
}

// WithScheduleID 按日程 ID 过滤查询结果。
func WithScheduleID(id int) QueryOption {
	return func(query *Option) { query.id = &id }
}

// WithScheduleName 按名称包含关系过滤查询结果。
func WithScheduleName(name string) QueryOption {
	return func(query *Option) { query.name = &name }
}

// WithScheduleType 按日程类型过滤查询结果。
func WithScheduleType(scheduleType ScheduleType) QueryOption {
	return func(query *Option) { query.scheduleType = &scheduleType }
}

// WithScheduleLimit 设置查询结果的最大条数。
func WithScheduleLimit(limit int) QueryOption {
	return func(query *Option) { query.limit = &limit }
}

// WithScheduleOffset 设置查询结果跳过的条数。
func WithScheduleOffset(offset int) QueryOption {
	return func(query *Option) { query.offset = &offset }
}

// WithScheduleIncludeDeleted 控制查询是否包含已软删除日程。
func WithScheduleIncludeDeleted(include bool) QueryOption {
	return func(query *Option) { query.includeDeleted = include }
}

func (query Option) ID() (int, bool) {
	if query.id == nil {
		return 0, false
	}
	return *query.id, true
}

func (query Option) Name() (string, bool) {
	if query.name == nil {
		return "", false
	}
	return *query.name, true
}

func (query Option) Offset() (int, bool) {
	if query.offset == nil {
		return 0, false
	}
	return *query.offset, true
}

func (query Option) Limit() (int, bool) {
	if query.limit == nil {
		return 0, false
	}
	return *query.limit, true
}

func (query Option) Type() (ScheduleType, bool) {
	if query.scheduleType == nil {
		return "", false
	}
	return *query.scheduleType, true
}

func (query Option) IncludeDeleted() bool { return query.includeDeleted }

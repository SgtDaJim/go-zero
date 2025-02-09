package logx

type LogRotationRuleType int

const (
	LogRotationRuleTypeDaily LogRotationRuleType = iota
	LogRotationRuleTypeSizeLimit
)

// A LogConf is a logging config.
type LogConf struct {
	ServiceName         string `json:",optional"`
	Mode                string `json:",default=console,options=[console,file,volume]"`
	Encoding            string `json:",default=json,options=[json,plain]"`
	TimeFormat          string `json:",optional"`
	Path                string `json:",default=logs"`
	Level               string `json:",default=info,options=[info,error,severe]"`
	Compress            bool   `json:",optional"`
	KeepDays            int    `json:",optional"`
	StackCooldownMillis int    `json:",default=100"`
	// MaxBackups represents how many backup log files will be kept. 0 means all files will be kept forever.
	// Only take effect when RotationRuleType is `LogRotationRuleTypeSizeLimit`
	// NOTE: the level of option `KeepDays` will be higher. Even thougth `MaxBackups` sets 0, log files will
	// still be removed if the `KeepDays` limitation is reached.
	MaxBackups int `json:",default=0"`
	// MaxSize represents how much space the writing log file takes up. 0 means no limit. The unit is `MB`.
	// Only take effect when RotationRuleType is `LogRotationRuleTypeSizeLimit`
	MaxSize int `json:",default=0"`
	// RotationRuleType represents the type of log rotation rule. Default is DailyRotateRule.
	// 0: LogRotationRuleTypeDaily
	// 1: LogRotationRuleTypeSizeLimit
	RotationRuleType LogRotationRuleType `json:",default=0,options=[0,1]"`
}

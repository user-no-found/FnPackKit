package backend

import "errors"

var (
	ErrConnectionRequired    = errors.New("management connection is not configured")
	ErrConnectionInvalid     = errors.New("local application connection configuration is invalid")
	ErrControlAuthentication = errors.New("management authentication failed")
	ErrControlUnavailable    = errors.New("management service is unreachable")
	ErrControlResponse       = errors.New("management response is invalid")
	ErrUpdateNotConfigured   = errors.New("native update action is not configured")
)

// Problem contains fixed public diagnostics, never local error text or secrets.
type Problem struct {
	Code             string `json:"code"`
	Message          string `json:"message"`
	PasswordRequired bool   `json:"password_required,omitempty"`
}

func DescribeFailure(err error) *Problem {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrUpdateNotConfigured):
		return &Problem{"update_not_configured", "本地管理器尚未配置此更新动作，请配置原生更新命令。", false}
	case errors.Is(err, ErrConnectionInvalid):
		return &Problem{"connection_invalid", "本地应用引用或管理配置无效，请检查 bridge-link.json 与原生管理配置。", false}
	case errors.Is(err, ErrConnectionRequired):
		return &Problem{"connection_required", "尚未连接本地管理服务。输入当前 NAS 账号的 sudo 密码后修复连接并继续。", true}
	case errors.Is(err, ErrControlAuthentication):
		return &Problem{"authentication_failed", "授权未通过或管理凭据已失效。请重新输入当前 NAS 账号的 sudo 密码。", true}
	case errors.Is(err, ErrControlUnavailable):
		return &Problem{"management_unreachable", "本地管理服务无法连接，请检查管理服务是否运行。", false}
	case errors.Is(err, ErrControlResponse):
		return &Problem{"management_invalid_response", "本地管理服务返回了无效状态，请检查管理接口与运行版本。", false}
	default:
		return &Problem{"operation_failed", "操作未完成，请刷新状态并检查本地管理日志。", false}
	}
}

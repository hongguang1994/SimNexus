package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
)

// R 是统一 API 响应结构。code=0 表示成功，非 0 表示业务错误。
type R struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

// OK 返回成功响应，data 为业务数据。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, R{Code: 0, Msg: "ok", Data: data})
}

// Fail 返回业务错误响应，httpStatus 为 HTTP 状态码，code 为业务错误码，msg 为错误描述。
func Fail(c *gin.Context, httpStatus int, code int, msg string) {
	c.JSON(httpStatus, R{Code: code, Msg: msg, Data: nil})
}

// reModem 从 D-Bus 路径（如 /org/freedesktop/ModemManager1/Modem/3）提取数字索引。
var reModem = regexp.MustCompile(`/Modem/(\d+)$`)

// derefBool 安全解引用 *bool，nil 视为 false。
func derefBool(b *bool) bool {
	return b != nil && *b
}

// remarshal 将任意值经 JSON 编解码转换为目标类型，常用于 map→struct 的宽松绑定。
func remarshal(src interface{}, dst interface{}) {
	b, _ := json.Marshal(src)
	json.Unmarshal(b, dst)
}

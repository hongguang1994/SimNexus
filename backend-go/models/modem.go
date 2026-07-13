package models

import "time"

// Modem 状态常量，由轮询器写入并通过 WebSocket 推送给前端。
const (
	ModemConnected    = "connected"    // 已注册/连接
	ModemDisconnected = "disconnected" // 断线/禁用/搜网中
	ModemError        = "error"        // 故障状态
	ModemUnknown      = "unknown"      // 状态未知
)

// Modem 表示一个 USB 调制解调器（或 ZTE 便携 WiFi 设备）的持久化状态。
// MmObjectPath 是唯一键：mmcli D-Bus 路径，如 /org/freedesktop/ModemManager1/Modem/0；
// ZTE 设备使用合成路径 zte:192.168.0.1。
type Modem struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	DevicePath   string `gorm:"size:100" json:"device_path"`                                               // 设备节点，如 ttyUSB0
	MmObjectPath string `gorm:"column:mm_object_path;size:200;uniqueIndex;not null" json:"mm_object_path"` // 唯一键
	Imei         string `gorm:"size:20;uniqueIndex" json:"imei"`                                           // IMEI，可能为空
	Manufacturer string `gorm:"size:100" json:"manufacturer"`
	Model        string `gorm:"size:100" json:"model"`
	PhoneNumber  string `gorm:"size:30" json:"phone_number"`
	Operator     string `gorm:"size:100" json:"operator"` // 运营商名称

	SignalQuality int       `gorm:"default:0" json:"signal_quality"` // 信号质量 0-100
	Status        string    `gorm:"size:20;default:unknown" json:"status"`
	Alias         string    `gorm:"size:100" json:"alias"`         // 用户自定义别名
	IsActive      bool      `gorm:"default:true" json:"is_active"` // false 表示设备已拔出
	LastSeen      time.Time `json:"last_seen"`
	CreatedAt     time.Time `json:"created_at"`

	AccessTechnologies string `gorm:"size:100" json:"access_technologies"` // 接入技术，如 lte
	RegistrationState  string `gorm:"size:50" json:"registration_state"`
	TxBytes            int64  `gorm:"default:0" json:"tx_bytes"`
	RxBytes            int64  `gorm:"default:0" json:"rx_bytes"`
	ConnectionDuration int64  `gorm:"default:0" json:"connection_duration"` // 秒

	Imsi             string `gorm:"size:20" json:"imsi"`
	Iccid            string `gorm:"size:30" json:"iccid"`
	FirmwareRevision string `gorm:"size:100" json:"firmware_revision"`
	HardwareRevision string `gorm:"size:50" json:"hardware_revision"`
	CurrentBands     string `gorm:"size:500" json:"current_bands"`
	SimOperatorName  string `gorm:"size:100" json:"sim_operator_name"` // SIM 卡归属运营商名
	SimOperatorCode  string `gorm:"size:20" json:"sim_operator_code"`  // MCC+MNC
	CurrentModes     string `gorm:"size:200" json:"current_modes"`
	Ports            string `gorm:"size:300" json:"ports"`
	Plugin           string `gorm:"size:50" json:"plugin"` // mmcli 插件名

	// VoWiFi（Wi-Fi Calling）相关：VowifiMode=true 时该卡改走自建 VoWiFi 协议栈
	// （IKEv2/EAP-AKA/IMS-ESP）收发短信，而非 mmcli。进入该模式要求该卡不再由
	// ModemManager 管理（独占串口 + 裸 socket）。
	VowifiMode     bool   `gorm:"default:false" json:"vowifi_mode"`
	VowifiEpdgIP   string `gorm:"size:64" json:"vowifi_epdg_ip"` // ePDG IPv4（留空则用环境变量 VOWIFI_EPDG_IP）
	VowifiATPort   string `gorm:"size:64" json:"vowifi_at_port"` // 串口设备，如 /dev/ttyUSB2（留空则用 VOWIFI_AT_PORT）
	VowifiAirplane bool   `json:"vowifi_airplane"`               // 飞行模式：关射频(CFUN=4)不在蜂窝注册。默认 false，避免新卡被自动关射频
}

func (Modem) TableName() string { return "modems" }

package vowifi

// 建立 VoWiFi 通道的分步骤状态追踪：供上层（VowifiManager / 前端）一眼看出通道
// 建立到哪一步、哪一步健康、哪一步失败。Session 在各阶段调用 step() 记录。

import (
	"sync"
	"time"
)

// 步骤状态
const (
	StepPending = "pending" // 未开始
	StepRunning = "running" // 进行中
	StepOK      = "ok"      // 成功
	StepFail    = "fail"    // 失败
)

// 步骤名（顺序即通道建立顺序）
const (
	StepInhibit   = "独占该卡"       // mmcli --inhibit 释放串口
	StepIKEInit   = "IKE_SA_INIT"    // IKEv2 初始交换
	StepEAPAKA    = "EAP-AKA 认证"   // USIM 鉴权
	StepIKEAuth   = "IKE_AUTH 地址分配" // 拿到内网 IPv6 + P-CSCF
	StepRegister  = "IMS 注册"       // REGISTER 200 OK
	StepSubscribe = "SUBSCRIBE 订阅" // reg event 订阅
	StepReceiver  = "接收循环"        // 常驻接收就绪
)

// StepOrder 是步骤展示顺序。
var StepOrder = []string{StepInhibit, StepIKEInit, StepEAPAKA, StepIKEAuth, StepRegister, StepSubscribe, StepReceiver}

// Step 单个步骤的状态。
type Step struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	At     string `json:"at"`
}

// Steps 线程安全地保存一次通道建立的分步状态。
type Steps struct {
	mu sync.Mutex
	m  map[string]Step
}

// NewSteps 创建一个步骤追踪器，并把已知步骤初始化为 pending。
func NewSteps() *Steps {
	s := &Steps{m: map[string]Step{}}
	for _, n := range StepOrder {
		s.m[n] = Step{Name: n, State: StepPending}
	}
	return s
}

// Set 记录某步骤的最新状态。
func (s *Steps) Set(name, state, detail string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = Step{Name: name, State: state, Detail: detail, At: time.Now().Format("15:04:05")}
}

// Snapshot 按 StepOrder 返回步骤列表快照。
func (s *Steps) Snapshot() []Step {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Step, 0, len(StepOrder))
	for _, n := range StepOrder {
		if st, ok := s.m[n]; ok {
			out = append(out, st)
		} else {
			out = append(out, Step{Name: n, State: StepPending})
		}
	}
	return out
}

// attachSteps 让 Session 关联一个步骤追踪器。
func (s *Session) attachSteps(st *Steps) { s.steps = st }

// step 记录一个步骤状态（Session 内部各阶段调用）。
func (s *Session) step(name, state, detail string) {
	if s.steps != nil {
		s.steps.Set(name, state, detail)
	}
}

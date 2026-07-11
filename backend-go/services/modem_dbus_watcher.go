package services

import (
	"context"
	"log/slog"
	"time"

	"github.com/godbus/dbus/v5"
)

// mmObjectManagerPath 是 ModemManager 对象管理器路径，设备增删事件从这里广播。
const mmObjectManagerPath = "/org/freedesktop/ModemManager1"

// watchModemDBusEvents 订阅 ModemManager 的 D-Bus 信号（设备增删、属性变化），
// 收到信号后通过 trigger 通知轮询循环立即执行一次扫描，实现近实时检测。
// 这是对定时轮询的增强而非替代：D-Bus 连接失败或信号丢失时，轮询仍会按固定周期兜底运行。
func watchModemDBusEvents(ctx context.Context, trigger chan<- struct{}) {
	conn, err := dbus.SystemBus()
	if err != nil {
		slog.Error("dbus watcher: 无法连接系统总线，将仅依赖定时轮询", "err", err)
		return
	}
	defer conn.Close()

	rules := []string{
		"type='signal',interface='org.freedesktop.DBus.ObjectManager',member='InterfacesAdded',path='" + mmObjectManagerPath + "'",
		"type='signal',interface='org.freedesktop.DBus.ObjectManager',member='InterfacesRemoved',path='" + mmObjectManagerPath + "'",
		"type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path_namespace='" + mmObjectManagerPath + "/Modem'",
	}
	for _, rule := range rules {
		if call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, rule); call.Err != nil {
			slog.Error("dbus watcher: 订阅信号失败，将仅依赖定时轮询", "rule", rule, "err", call.Err)
			return
		}
	}

	signals := make(chan *dbus.Signal, 20)
	conn.Signal(signals)
	slog.Info("dbus watcher: 已启动，实时监听 ModemManager 设备事件")

	// 短时间内的多个信号（如一次插拔触发的多条属性变化）合并为一次扫描，避免频繁重复轮询。
	const debounce = 500 * time.Millisecond
	timer := time.NewTimer(debounce)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false

	fire := func() {
		select {
		case trigger <- struct{}{}:
		default: // 已有一次待处理的触发，无需重复排队
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-signals:
			if !ok {
				return
			}
			_ = sig
			if !pending {
				pending = true
				timer.Reset(debounce)
			}
		case <-timer.C:
			if pending {
				pending = false
				fire()
			}
		}
	}
}

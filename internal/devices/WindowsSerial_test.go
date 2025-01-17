package devices

import (
	"go.bug.st/serial"
	"log"
	"testing"
)

func TestListSerial(t *testing.T) {
	open, _ := serial.Open("", &serial.Mode{
		BaudRate:          0,
		DataBits:          0,
		Parity:            0,
		StopBits:          0,
		InitialStatusBits: nil,
	})
	open.Close()

	// 列出可用串口设备
	ports, err := serial.GetPortsList()
	if err != nil {
		log.Println("无法列出串口设备:", err)
		return
	}

	if len(ports) == 0 {
		log.Println("未找到可用串口设备")
		return
	}

	log.Println("可用串口设备:")
	for _, port := range ports {
		log.Println(port)
	}
}

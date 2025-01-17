package devices

import (
	"github.com/tarm/serial"
	"log"
	"sync"
)

const (
	DEVICE_UNKNOWN = iota
	DEVICE_SERVER
	DEVICE_BEIDOU
	DEVICE_TIANTONG
	DEVICE_LOCAL_SERIAL
	DEVICE_YARK
)

type DeviceUtil struct {
	Device         Device
	DeviceType     uint8
	DeviceTypeName string
}

var lock sync.Mutex
var DeviceMap = make(map[uint8]*DeviceUtil)

func RegisterDevice(deviceType uint8, deviceUtil *DeviceUtil) {
	lock.Lock()
	defer lock.Unlock()
	if DeviceMap[deviceType] == nil {
		deviceUtil.DeviceType = deviceType
		DeviceMap[deviceType] = deviceUtil
	}
	log.Print("Device ", deviceUtil.DeviceTypeName, " registered!")
}

// Device 一般为资源受限的设备，需要发送字节流，但服务器的资源不受限，直接发送CoT的xml
type Device interface {
	GetType() int
	GetMaxLength() int
	Send(content string) error //发送字符串，一般来说需要对传入的字符串进行处理，然后调用SendByte执行真正的发送逻辑
	SendByte(byteContent []byte) error
	Recv(callback func(message []byte)) error
}

// SerialDevice means the local usb serial device, it should be able to connect or disconnect
type SerialDevice interface {
	GetConfig() *serial.Config
	SetConfig(config *serial.Config)
	Connect() error
	Disconnect()
	IsConnect() bool
}

package devices

import (
	"fmt"
	message "github.com/kdudkov/goasae/internal/msg_converter"
	"github.com/tarm/serial"
	"log"
	"log/slog"
	"strconv"
	"time"
)

func init() {
	RegisterDevice(DEVICE_LOCAL_SERIAL, &DeviceUtil{
		Device: &LocalSerial{
			buffer:          []byte{},
			port:            nil,
			config:          nil,
			keepConnect:     false,
			messageCallback: nil,
		},
		DeviceType:     DEVICE_LOCAL_SERIAL,
		DeviceTypeName: "LocalSerial",
	})
}

type LocalSerial struct {
	buffer          []byte
	port            *serial.Port
	config          *serial.Config
	keepConnect     bool
	messageCallback func(msg []byte)
	isConnect       bool
}

func (localSerial *LocalSerial) GetType() int {
	return DEVICE_LOCAL_SERIAL
}

func (localSerial *LocalSerial) GetMaxLength() int {
	return 0x7FFF
}
func (localSerial *LocalSerial) IsConnect() bool {
	return localSerial.isConnect
}

func (localSerial *LocalSerial) Send(content string) error {
	return fmt.Errorf("unimplemented")
}

func (localSerial *LocalSerial) SendByte(byteContent []byte) error {
	if len(byteContent) > localSerial.GetMaxLength() {
		return fmt.Errorf("数据长度超过当前设备限制，发送已取消")
	}
	if !localSerial.keepConnect {
		localSerial.isConnect = false
		return fmt.Errorf("串口断开")
	}
	_, err := localSerial.port.Write(byteContent)
	return err
}

//
//func GetGid() (gid uint64) {
//	b := make([]byte, 64)
//	b = b[:runtime.Stack(b, false)]
//	b = bytes.TrimPrefix(b, []byte("goroutine "))
//	b = b[:bytes.IndexByte(b, ' ')]
//	n, err := strconv.ParseUint(string(b), 10, 64)
//	if err != nil {
//		panic(err)
//	}
//	return n
//}

func (localSerial *LocalSerial) Recv(callback func(message []byte)) error {
	if !localSerial.keepConnect {
		localSerial.isConnect = false
		return fmt.Errorf("串口断开")
	}
	var err error
	if callback == nil {
		callback = localSerial.messageCallback
	}
	tmp := make([]byte, 128)
	go func() {
		expectedLen := message.ErrUnknownMsgLen // 初始长度为未知数值
		lastTs := time.Now().Unix()
		for localSerial.keepConnect {
			//slog.Info("waiting for data at go routine " + strconv.Itoa(int(GetGid())))
			read, err := localSerial.port.Read(tmp)
			// 等待时间过长，清空缓存
			if time.Now().Unix()-lastTs > 2 && expectedLen != message.ErrUnknownMsgLen {
				slog.Warn("Timeout! Clear msg buffer to avoid potential errors")
				expectedLen = message.ErrUnknownMsgLen
				localSerial.buffer = localSerial.buffer[:0]
			} else {
				lastTs = time.Now().Unix()
			}
			slog.Info("read data of length " + strconv.Itoa(read))
			if err != nil {
				err = localSerial.Connect()
			}
			localSerial.buffer = append(localSerial.buffer, tmp[:read]...)
			if expectedLen == message.ErrUnknownMsgLen {
				expectedLen, err = message.GetMessageExpectedLength(localSerial.buffer)
				if err != nil {
					if expectedLen == message.ErrMsgTooShort { // -1 表示消息过短
						expectedLen = message.ErrUnknownMsgLen
						continue
					}
					slog.Error(err.Error())
					localSerial.buffer = localSerial.buffer[:0] // 清空切片
					expectedLen = message.ErrUnknownMsgLen
					continue
				}
			}
			if len(localSerial.buffer) < expectedLen {
				continue
			}
			// 消息长度大于等于期望值，取出消息，调用回调函数，然后将期望值重置为初值
			callback(localSerial.buffer[:expectedLen])
			localSerial.buffer = localSerial.buffer[expectedLen:]
			expectedLen = message.ErrUnknownMsgLen
		}
	}()
	return err
}

func (localSerial *LocalSerial) GetConfig() *serial.Config {
	return localSerial.config
}

func (localSerial *LocalSerial) SetConfig(config *serial.Config) {
	localSerial.config = config
}

func (localSerial *LocalSerial) Connect() error {
	localSerial.keepConnect = true
	if localSerial.config == nil {
		return fmt.Errorf("当前配置不正确")
	} else {
		log.Println("当前串口配置: ", localSerial.config)
	}
	var err error
	for localSerial.keepConnect {
		localSerial.isConnect = false
		log.Println("尝试连接...")
		localSerial.port, err = serial.OpenPort(localSerial.config)
		if err == nil {
			localSerial.isConnect = true
			log.Println("已连接")
			return err
		}
		log.Println("错误: ", err)
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("连接被命令终止")
}

func (localSerial *LocalSerial) Disconnect() {
	localSerial.keepConnect = false
	localSerial.isConnect = false
	if localSerial.port != nil {
		localSerial.port.Close()
		localSerial.port = nil
	}
}

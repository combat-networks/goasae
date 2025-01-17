package main

import (
	"context"
	"fmt"
	"github.com/kdudkov/goasae/internal/client"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// fed server 作为服务器，主要工作是为客户端提供数据，但客户端不会向服务端传输数据，如有此需求应当配置双向连接

func (app *App) ListenTcpFed(ctx context.Context, addr string) (err error) {
	app.logger.Info("listening TCP Federation at " + addr)
	defer func() {
		if r := recover(); r != nil {
			app.logger.Error("panic in ListenTCP", slog.Any("error", r))
		}
	}()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		app.logger.Error("Failed to listen", slog.Any("error", err))

		return err
	}

	defer listener.Close()

	for ctx.Err() == nil {
		conn, err := listener.Accept()
		if err != nil {
			app.logger.Error("Unable to accept connections", slog.Any("error", err))

			return err
		}

		remoteAddr := conn.RemoteAddr().String()
		localAddr := conn.LocalAddr().String()
		app.logger.Info("TCP Federation connection from " + remoteAddr)
		h := client.NewConnClientHandler(
			conn.RemoteAddr().Network()+"_"+remoteAddr,
			conn, &client.HandlerConfig{
				// 创建一个处理客户端请求的功能，不要接收来自它的消息，但需要把消息发给它
				MessageCb:    app.NewCotMessage,
				RemoveCb:     app.RemoveHandlerCb,
				NewContactCb: app.NewContactCb,
				DropMetric:   dropMetric,
				Name:         fmt.Sprintf("fed_%s:%v", strings.Split(remoteAddr, ":")[0], strings.Split(localAddr, ":")[1]),
			})
		app.AddClientHandler(h)
		h.Start()
	}

	return nil
}

// ConnectToFedServer 这是Fed服务器，只应该从服务器获取信息，但不要发送任何信息过去
func (app *App) ConnectToFedServer(ctx context.Context, fed *FedConfig) {
	for ctx.Err() == nil {
		addr := fmt.Sprintf("%s:%d:%s", fed.Host, fed.Port, fed.Proto) // localhost:8087:tcp
		conn, err := app.connect(addr)
		if err != nil {
			app.logger.Error("Fed Server connect error", slog.Any("error", err))
			time.Sleep(time.Second * 5)
			continue
		}

		fedName := fmt.Sprintf("fed_%s:%v", fed.Host, fed.Port)
		app.logger.Info(fmt.Sprintf("Federation to %s connected", fedName))

		wg := &sync.WaitGroup{}
		wg.Add(1)

		h := client.NewConnClientHandler(addr, conn, &client.HandlerConfig{
			MessageCb: app.NewCotMessage,
			RemoveCb: func(ch client.ClientHandler) {
				wg.Done()
				app.handlers.Delete(addr)
				app.logger.Info("disconnected")
			},
			NewContactCb: app.NewContactCb,
			Name:         fedName,
			DisableSend:  fed.DisableSend,
			DisableRecv:  fed.DisableReceive,
			IsClient:     true,
			UID:          app.uid,
		})

		go h.Start()
		app.AddClientHandler(h)

		wg.Wait()
	}
}

func (app *App) connToFed(ctx context.Context, fed *FedConfig) error {
	app.ConnectToFedServer(ctx, fed)
	return nil
}

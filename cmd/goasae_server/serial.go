package main

import (
	"context"
	"github.com/kdudkov/goasae/internal/client"
	"github.com/kdudkov/goasae/internal/devices"
	"github.com/tarm/serial"
	"sync"
)

func (app *App) ConnectToSerials(ctx context.Context, serials *[]string) {
	if serials == nil || len(*serials) <= 0 {
		return
	}
	for ctx.Err() == nil {
		wg := &sync.WaitGroup{}
		wg.Add(len(*serials))
		for _, serialName := range *serials {
			localSerial := devices.LocalSerial{}
			localSerial.SetConfig(&serial.Config{Name: serialName, Baud: 9600})
			h := &client.SerialClientHandler{
				Serial:   &localSerial,
				NewMsgCb: app.NewCotMessage,
				RemoveCb: func(ch client.ClientHandler) {
					wg.Done()
					app.handlers.Delete(ch.GetIdentifier())
					app.logger.Info("serial client disconnected")
				},
			}
			h.Start()
			app.AddClientHandler(h)
		}
		wg.Wait()
	}
}

package main

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/kdudkov/goutils/callback"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/kdudkov/goasae/cmd/goasae_server/missions"
	"github.com/kdudkov/goasae/internal/client"
	im "github.com/kdudkov/goasae/internal/model"
	"github.com/kdudkov/goasae/internal/pm"
	"github.com/kdudkov/goasae/internal/repository"
	"github.com/kdudkov/goasae/pkg/cot"
	"github.com/kdudkov/goasae/pkg/model"
	"github.com/kdudkov/goasae/pkg/tlsutil"
)

const (
	dbName = "db.sqlite"
)

var (
	lastSeenOfflineTimeout = time.Minute * 5
)

type FedConfig struct {
	Host           string `mapstructure:"host"`
	Proto          string `mapstructure:"proto"`
	Port           int    `mapstructure:"port"`
	Name           string `mapstructure:"name"`
	DisableSend    bool   `mapstructure:"disableSend,default=false"`
	DisableReceive bool   `mapstructure:"disableReceive,default=false"`
}

type AppConfig struct {
	udpAddr    string
	tcpAddr    string
	tcpFedAddr string
	adminAddr  string
	apiAddr    string
	certAddr   string
	tlsAddr    string

	feds *[]FedConfig

	usersFile string

	dataDir string

	logging    bool
	tlsCert    *tls.Certificate
	certPool   *x509.CertPool
	serverCert *x509.Certificate
	ca         []*x509.Certificate

	useSsl bool

	webtakRoot string

	debug    bool
	dataSync bool

	certTTLDays int
	connections []string

	serials []string
}

type App struct {
	logger         *slog.Logger
	packageManager pm.PackageManager
	config         *AppConfig
	lat            float64
	lon            float64
	zoom           int8

	handlers sync.Map

	changeCb *callback.Callback[*model.Item]
	deleteCb *callback.Callback[string]

	items    repository.ItemsRepository
	messages []*model.ChatMessage
	feeds    repository.FeedsRepository
	missions *missions.MissionManager

	users repository.UserRepository

	uid             string
	ch              chan *cot.CotMessage
	eventProcessors []*EventProcessor
}

func NewApp(config *AppConfig) *App {
	app := &App{
		logger:          slog.Default(),
		config:          config,
		packageManager:  pm.NewPackageManager(filepath.Join(config.dataDir, "mp")),
		users:           repository.NewFileUserRepo(config.usersFile),
		ch:              make(chan *cot.CotMessage, 100),
		handlers:        sync.Map{},
		changeCb:        callback.New[*model.Item](),
		deleteCb:        callback.New[string](),
		items:           repository.NewItemsMemoryRepo(),
		feeds:           repository.NewFeedsFileRepo(filepath.Join(config.dataDir, "feeds")),
		uid:             uuid.NewString(),
		eventProcessors: make([]*EventProcessor, 0),
	}

	if app.config.dataSync {
		db, err := getDatabase()

		if err != nil {
			panic(err)
		}

		app.missions = missions.New(db)
		if err := app.missions.Migrate(); err != nil {
			panic(err)
		}
	}

	return app
}

func (app *App) Run() {
	app.InitMessageProcessors()

	if app.users != nil {
		if err := app.users.Start(); err != nil {
			log.Fatal(err)
		}
	}

	if app.feeds != nil {
		if err := app.feeds.Start(); err != nil {
			log.Fatal(err)
		}
	}

	if err := app.packageManager.Start(); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	if app.config.udpAddr != "" {
		go func() {
			if err := app.ListenUDP(ctx, app.config.udpAddr); err != nil {
				panic(err)
			}
		}()
	}

	if app.config.tcpAddr != "" {
		go func() {
			if err := app.ListenTCP(ctx, app.config.tcpAddr); err != nil {
				panic(err)
			}
		}()
	}

	if app.config.tcpFedAddr != "" {
		go func() {
			if err := app.ListenTcpFed(ctx, app.config.tcpFedAddr); err != nil {
				panic(err)
			}
		}()
	}

	// 这里配置连接到fed服务器的逻辑
	if app.config.feds != nil {
		for _, fed := range *app.config.feds {
			go func() {
				if err := app.connToFed(ctx, &fed); err != nil {
					panic(err)
				}
			}()
		}
	}

	// 连接串口设备
	if app.config.serials != nil {
		go func() {
			app.ConnectToSerials(ctx, &app.config.serials)
		}()
	}

	if app.config.tlsCert != nil && app.config.tlsAddr != "" {
		go func() {
			if err := app.listenTLS(ctx, app.config.tlsAddr); err != nil {
				panic(err)
			}
		}()
	}

	NewHttp(app).Start()

	go app.messageProcessLoop()
	go app.cleaner()

	for _, c := range app.config.connections {
		app.logger.Info("start external connection to " + c)
		go app.ConnectTo(ctx, c)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	app.logger.Info("exiting...")
	cancel()
}

func (app *App) DummyHandler(msg *cot.CotMessage) {}

func (app *App) NewCotMessage(msg *cot.CotMessage) {
	if msg != nil {
		t := msg.GetType()

		if strings.HasPrefix(t, "a-") && len(t) > 5 {
			t = t[:5]
		}

		messagesMetric.With(prometheus.Labels{"scope": msg.Scope, "msg_type": t}).Inc()

		select {
		case app.ch <- msg:
		default:
			dropMetric.With(prometheus.Labels{"scope": msg.Scope, "reason": "main_ch"}).Inc()
		}
	}
}

func (app *App) AddClientHandler(ch client.ClientHandler) {
	app.handlers.Store(ch.GetIdentifier(), ch)
	connectionsMetric.With(prometheus.Labels{"scope": ch.GetUser().GetScope()}).Inc()
}

func (app *App) RemoveClientHandler(name string) {
	if v, ok := app.handlers.LoadAndDelete(name); ok {
		app.logger.Info("remove handler: " + name)
		ch := v.(client.ClientHandler)
		connectionsMetric.With(prometheus.Labels{"scope": ch.GetUser().GetScope()}).Dec()
	}
}

func (app *App) ForAllClients(f func(ch client.ClientHandler) bool) {
	app.handlers.Range(func(_, value any) bool {
		h := value.(client.ClientHandler)
		return f(h)
	})
}

func (app *App) RemoveHandlerCb(cl client.ClientHandler) {
	app.RemoveClientHandler(cl.GetIdentifier())

	for uid := range cl.GetUids() {
		if c := app.items.Get(uid); c != nil {
			c.SetOffline()
		}

		msg := &cot.CotMessage{
			From:       cl.GetName(),
			Scope:      cl.GetUser().GetScope(),
			TakMessage: cot.MakeOfflineMsg(uid, ""),
		}
		app.NewCotMessage(msg)
	}
}

func (app *App) NewContactCb(uid, callsign string) {
	app.logger.Info(fmt.Sprintf("new contact: %s %s", uid, callsign))
}

func (app *App) ConnectTo(ctx context.Context, addr string) {
	name := "ext_" + addr

	for ctx.Err() == nil {
		conn, err := app.connect(addr)
		if err != nil {
			app.logger.Error("connect error", slog.Any("error", err))
			time.Sleep(time.Second * 5)

			continue
		}

		app.logger.Info("connected")

		wg := &sync.WaitGroup{}
		wg.Add(1)

		h := client.NewConnClientHandler(name, conn, &client.HandlerConfig{
			MessageCb: app.NewCotMessage,
			RemoveCb: func(ch client.ClientHandler) {
				wg.Done()
				app.handlers.Delete(name)
				app.logger.Info("disconnected")
			},
			IsClient: true,
			UID:      app.uid,
		})

		go h.Start()
		app.AddClientHandler(h)

		wg.Wait()
	}
}

func (app *App) connect(connectStr string) (net.Conn, error) {
	parts := strings.Split(connectStr, ":")

	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid connect string: %s", connectStr)
	}

	var tlsConn bool

	switch parts[2] {
	case "tcp":
		tlsConn = false
	case "ssl":
		tlsConn = true
	default:
		return nil, fmt.Errorf("invalid connect string: %s", connectStr)
	}

	addr := fmt.Sprintf("%s:%s", parts[0], parts[1])

	if tlsConn {
		app.logger.Info(fmt.Sprintf("connecting with SSL to %s...", connectStr))

		conn, err := tls.Dial("tcp", addr, app.getTLSConfig())
		if err != nil {
			return nil, err
		}

		app.logger.Debug("handshake...")

		if err := conn.Handshake(); err != nil {
			return conn, err
		}

		cs := conn.ConnectionState()

		app.logger.Info(fmt.Sprintf("Handshake complete: %t", cs.HandshakeComplete))
		app.logger.Info(fmt.Sprintf("version: %d", cs.Version))

		for i, cert := range cs.PeerCertificates {
			app.logger.Info(fmt.Sprintf("cert #%d subject: %s", i, cert.Subject.String()))
			app.logger.Info(fmt.Sprintf("cert #%d issuer: %s", i, cert.Issuer.String()))
			app.logger.Info(fmt.Sprintf("cert #%d dns_names: %s", i, strings.Join(cert.DNSNames, ",")))
		}

		return conn, nil
	}

	app.logger.Info(fmt.Sprintf("connecting to %s...", connectStr))

	return net.DialTimeout("tcp", addr, time.Second*3)
}

func (app *App) getTLSConfig() *tls.Config {
	p12Data, err := os.ReadFile(viper.GetString("ssl.cert"))
	if err != nil {
		app.logger.Error(err.Error())
		panic(err)
	}

	key, cert, _, err := pkcs12.DecodeChain(p12Data, viper.GetString("ssl.password"))
	if err != nil {
		app.logger.Error(err.Error())
		panic(err)
	}

	tlsCert := tls.Certificate{ //nolint:exhaustruct,typeassert
		Certificate: [][]byte{cert.Raw},
		PrivateKey:  key.(crypto.PrivateKey),
		Leaf:        cert,
	}

	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, InsecureSkipVerify: true} //nolint:exhaustruct
}

func (app *App) messageProcessLoop() {
	for msg := range app.ch {
		app.processMessage(msg)
	}
}

func (app *App) route(msg *cot.CotMessage) bool {
	if missions := msg.GetDetail().GetDestMission(); len(missions) > 0 {
		app.logger.Debug(fmt.Sprintf("point %s %s: missions: %s", msg.GetUID(), msg.GetCallsign(), strings.Join(missions, ",")))

		for _, missionName := range missions {
			app.processMissionPoint(missionName, msg)
		}

		return true
	}

	if dest := msg.GetDetail().GetDestCallsign(); len(dest) > 0 {
		for _, s := range dest {
			app.sendToCallsign(s, msg)
		}

		return true
	}

	app.sendBroadcast(msg)

	return true
}

func (app *App) processMissionPoint(missionName string, msg *cot.CotMessage) {
	m := app.missions.GetMission(msg.Scope, missionName)

	if m == nil {
		return
	}

	var change *im.Change

	if msg.GetType() == "t-x-d-d" {
		if uid := msg.GetFirstLink("p-p").GetAttr("uid"); uid != "" {
			change = app.missions.DeleteMissionPoint(m.ID, uid, "")
		}
	} else {
		change = app.missions.AddPoint(m, msg)
	}

	if change != nil {
		app.notifyMissionSubscribers(m, change)
	}
}

func (app *App) notifyMissionSubscribers(mission *im.Mission, c *im.Change) {
	if mission == nil || c == nil {
		return
	}

	msg := im.MissionChangeNotificationMsg(mission.Name, mission.Scope, c)
	for _, uid := range app.missions.GetSubscribers(mission.ID) {
		app.sendToUID(uid, msg)
	}
}

func (app *App) cleaner() {
	for range time.Tick(time.Minute) {
		app.cleanOldUnits()
	}
}

func (app *App) cleanOldUnits() {
	toDelete := make([]string, 0)

	app.items.ForEach(func(item *model.Item) bool {
		switch item.GetClass() {
		case model.UNIT, model.POINT:
			if item.IsOld() {
				toDelete = append(toDelete, item.GetUID())
				app.logger.Debug(fmt.Sprintf("removing %s %s", item.GetClass(), item.GetUID()))
			}
		case model.CONTACT:
			if item.IsOld() {
				toDelete = append(toDelete, item.GetUID())
				app.logger.Debug("removing contact " + item.GetUID())
			} else if item.IsOnline() && item.GetLastSeen().Add(lastSeenOfflineTimeout).Before(time.Now()) {
				item.SetOffline()
				app.changeCb.AddMessage(item)
			}
		}

		return true
	})

	for _, uid := range toDelete {
		app.items.Remove(uid)
		app.deleteCb.AddMessage(uid)
	}
}

var msgLRU = NewLRUCache[cot.CotMessage](2 << 10)

func mayCauseBroadCastStorm(msg *cot.CotMessage) bool {
	//fmt.Printf("msg [%s@%s], send time = %v", msg.GetType(), msg.GetUID(), msg.GetSendTime())
	if cachedMsg, ok := msgLRU.get(msg.GetUID()); !ok {
		// 这里设定了map的最大限制，不用担心内存泄露
		msgLRU.put(msg.GetUID(), msg)
	} else {
		// 如果消息间隔小于1s，则进行二次判断确认是否转发
		if msg.GetSendTime().Sub(cachedMsg.GetSendTime()) < time.Second {
			lat, lon := msg.GetLatLon()
			clat, clon := cachedMsg.GetLatLon()
			if lat == clat && lon == clon {
				// 如果此消息的经纬度与上次转发的消息相同，判定为重复，屏蔽转发
				slog.Warn(fmt.Sprintf("drop message [%s@%s] to avoid broadcast storm",
					msg.TakMessage.CotEvent.Type, msg.TakMessage.CotEvent.Uid))
				return true
			}
		}
		// 如果允许发送，要更新cot状态
		msgLRU.put(msg.GetUID(), msg)
	}
	return false
}

func (app *App) sendBroadcast(msg *cot.CotMessage) {
	if mayCauseBroadCastStorm(msg) {
		return
	}
	app.ForAllClients(func(ch client.ClientHandler) bool {
		// 需要判断是否允许向当前接口发送消息，以及是否是消息来源
		if (ch.CanSend() || msg.IsPing() || msg.IsControl()) &&
			(strings.HasPrefix(ch.GetName(), "fed_") || ch.GetName() != msg.From) {
			if err := ch.SendMsg(msg); err != nil {
				app.logger.Error(fmt.Sprintf("error sending to %s: %v", ch.GetName(), err))
			}
		}
		return true
	})
}

func (app *App) sendToCallsign(callsign string, msg *cot.CotMessage) {
	if mayCauseBroadCastStorm(msg) {
		return
	}

	app.ForAllClients(func(ch client.ClientHandler) bool {
		if !ch.CanSend() && !msg.IsPing() && !msg.IsControl() {
			return true
		}

		app.logger.Warn("sending", msg.Detail.AsXMLString(), "\nfrom", msg.From, "to", ch.GetName())

		// 对 fed 服务器的链路使用无条件广播，确保信息能传出
		if strings.HasPrefix(ch.GetName(), "fed_") || ch.GetName() != msg.From {
			if err := ch.SendMsg(msg); err != nil {
				app.logger.Error("send error", slog.Any("error", err))
			}
			return true
		}

		for _, c := range ch.GetUids() {
			if c == callsign {
				if err := ch.SendMsg(msg); err != nil {
					app.logger.Error("send error", slog.Any("error", err))
				}
			}
		}
		return true
	})
}

func (app *App) sendToUID(uid string, msg *cot.CotMessage) {
	if mayCauseBroadCastStorm(msg) {
		return
	}

	app.ForAllClients(func(ch client.ClientHandler) bool {
		if !ch.CanSend() && !msg.IsPing() && !msg.IsControl() {
			return true
		}
		if strings.HasPrefix(ch.GetName(), "fed_") || ch.GetName() != msg.From {
			if err := ch.SendMsg(msg); err != nil {
				app.logger.Error("send error", slog.Any("error", err))
			}
			return true
		}
		if ch.HasUID(uid) {
			if err := ch.SendMsg(msg); err != nil {
				app.logger.Error("send error", slog.Any("error", err))
			}
		}
		return true
	})
}

func loadPem(name string) ([]*x509.Certificate, error) {
	if name == "" {
		return nil, nil
	}

	pemBytes, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("error loading %s: %s", name, err.Error())
	}

	return tlsutil.DecodeAllCerts(pemBytes)
}

func processCerts(conf *AppConfig) error {
	for _, k := range []string{"ssl.ca", "ssl.cert", "ssl.key"} {
		if viper.GetString(k) == "" {
			return nil
		}
	}

	roots := x509.NewCertPool()
	conf.certPool = roots

	ca, err := loadPem(viper.GetString("ssl.ca"))
	if err != nil {
		return err
	}

	for _, c := range ca {
		roots.AddCert(c)
	}

	conf.ca = ca

	cert, err := loadPem(viper.GetString("ssl.cert"))
	if err != nil {
		return err
	}

	if len(cert) > 0 {
		conf.serverCert = cert[0]
	}

	for _, c := range cert {
		roots.AddCert(c)
	}

	tlsCert, err := tls.LoadX509KeyPair(viper.GetString("ssl.cert"), viper.GetString("ssl.key"))
	if err != nil {
		return err
	}

	conf.tlsCert = &tlsCert

	return nil
}

func getDatabase() (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}

	return db, nil
}

func main() {
	fmt.Printf("version %s\n", getVersion())
	//Linux改变工作目录
	err := os.Chdir("/root")
	if err!= nil {
		fmt.Println("Error changing working directory:", err)
		os.Exit(-1)
	}
	wd, err := os.Getwd()
	if err!= nil {
		fmt.Println("Error getting working directory:", err)
		os.Exit(-1)
	}
	fmt.Printf("Current working directory %s:\n", wd)

	conf := flag.String("config", "goasae_server.yml", "name of config file")
	flag.Parse()

	viper.SetConfigFile(*conf)

	viper.SetDefault("udp_addr", ":8999")
	viper.SetDefault("tcp_addr", ":8087")
	viper.SetDefault("ssl_addr", ":8089")
	viper.SetDefault("api_addr", ":8080")
	viper.SetDefault("log", false)
	viper.SetDefault("data_dir", "data")

	viper.SetDefault("me.lat", 59.8396)
	viper.SetDefault("me.lon", 31.0213)
	viper.SetDefault("users_file", "users.yml")

	viper.SetDefault("me.zoom", 10)
	viper.SetDefault("ssl.cert_ttl_days", 365)

	err = viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("Fatal error config file: %w \n", err))
	}

	flag.Parse()

	debug := viper.GetBool("debug")

	var h slog.Handler
	if debug {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}

	slog.SetDefault(slog.New(h))

	config := &AppConfig{
		udpAddr:     viper.GetString("udp_addr"),
		tcpAddr:     viper.GetString("tcp_addr"),
		tcpFedAddr:  viper.GetString("tcp_fed_addr"),
		adminAddr:   viper.GetString("admin_addr"),
		apiAddr:     viper.GetString("api_addr"),
		certAddr:    viper.GetString("cert_addr"),
		tlsAddr:     viper.GetString("ssl_addr"),
		useSsl:      viper.GetBool("ssl.use_ssl"),
		logging:     viper.GetBool("log"),
		dataDir:     viper.GetString("data_dir"),
		debug:       debug,
		connections: viper.GetStringSlice("connections"),
		serials:     viper.GetStringSlice("serials"),
		usersFile:   viper.GetString("users_file"),
		webtakRoot:  viper.GetString("webtak_root"),
		certTTLDays: viper.GetInt("ssl.cert_ttl_days"),
		dataSync:    viper.GetBool("datasync"),
		feds:        &[]FedConfig{},
	}

	feds, ok := viper.Get("feds").([]interface{})
	if ok && feds != nil {
		for _, fed := range feds {
			var fedConf FedConfig
			if err := decodeMapToStruct(&fed, &fedConf); err != nil {
				slog.Default().Error(err.Error())
			}
			*config.feds = append(*config.feds, fedConf)
		}
	} else {
		slog.Default().Info("no feds found in configuration")
	}

	if err := processCerts(config); err != nil {
		slog.Default().Error(err.Error())
	}

	app := NewApp(config)

	app.lat = viper.GetFloat64("me.lat")
	app.lon = viper.GetFloat64("me.lon")
	app.zoom = int8(viper.GetInt("me.zoom"))
	app.Run()
}

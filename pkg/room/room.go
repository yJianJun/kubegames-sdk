package room

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kubegames/kubegames-hall/app/model/game"
	modelRoom "github.com/kubegames/kubegames-hall/app/model/room"
	"github.com/kubegames/kubegames-operator/pkg/tools"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	"github.com/kubegames/kubegames-sdk/pkg/platform"
	"github.com/kubegames/kubegames-sdk/pkg/server"
	tableObject "github.com/kubegames/kubegames-sdk/pkg/table"
	"k8s.io/apimachinery/pkg/util/wait"
)

//interface that game logic needs to rement
type RoomHandler interface {
	//init table
	InitTable(table tableObject.TableInterface)
}

type RoomCheck interface {
	//check room
	CheckRoom(r *Room)
}

type DefaultRoomCheck struct {
}

//check room
func (d *DefaultRoomCheck) CheckRoom(r *Room) {
	rooms, err := r.Platform.FindRooms(context.Background(), r.Config.GameID)
	if err != nil {
		log.Warnf("find game %d rooms error %s", r.Config.GameID, err.Error())
		return
	}

	cache := make(map[uint32]*modelRoom.Room)

	//check open room
	for _, room := range rooms {
		cache[room.RoomID] = room

		//check is open
		if _, ok := r.Rooms.Load(room.RoomID); !ok {
			d.OpenRoom(r, room)
		}
	}

	//check close
	r.Rooms.Range(func(key, value interface{}) bool {
		roomid := key.(uint32)
		if _, ok := cache[roomid]; !ok {
			//close this room
			d.CloseRoom(r, roomid)
		}
		return true
	})
}

//open room
func (d *DefaultRoomCheck) OpenRoom(r *Room, room *modelRoom.Room) {
	//open room
	if _, err := r.Platform.RunningRoom(context.Background(), room.RoomID, fmt.Sprintf("%s:%d", r.Ip, r.Port)); err != nil {
		log.Warnf("running room %d server error %s", room.RoomID, err.Error())
		return
	}

	//set room
	r.Rooms.Store(room.RoomID, room)

	//open room
	log.Infof("open roomid %d config %s", room.RoomID, room)
	return
}

//close room
func (d *DefaultRoomCheck) CloseRoom(r *Room, roomid uint32) {
	//release room
	if _, err := r.Platform.ReleaseRoom(context.Background(), roomid, fmt.Sprintf("%s:%d", r.Ip, r.Port)); err != nil {
		log.Warnf("delete rooms %d server error %s", roomid, err.Error())
		return
	}

	//delete rooms
	r.Rooms.Delete(roomid)

	//range tables
	r.Tables.Range(func(key, value interface{}) bool {
		table, ok := value.(*tableObject.Table)
		if ok {
			if table.GetRoomID() == roomid {
				table.OnClose()
			}
		}
		return true
	})

	log.Infof("close room %d", roomid)
	return
}

type RoomOption func(r *Room)

func WithOptionRoomCheck(roomCheck RoomCheck) RoomOption {
	return func(r *Room) {
		r.RoomCheck = roomCheck
	}
}

//room r
type Room struct {
	Handler     RoomHandler
	PlayerCount int64
	TableCount  int64
	Tables      sync.Map
	Rooms       sync.Map
	Config      *game.GameConfig
	TableID     int64
	Platform    platform.Platform
	Ip          string
	Port        int
	Name        string
	StopCh      chan struct{}
	WaitClose   int32
	RoomCheck   RoomCheck
}

//new room
func NewRoom(handler RoomHandler, options ...RoomOption) *Room {
	//get port
	portstr := os.Getenv(tools.RunPort)
	if len(portstr) <= 0 {
		panic("port env is nil")
	}

	port, err := strconv.Atoi(portstr)
	if err != nil {
		panic(err)
	}

	ip := os.Getenv(tools.PodIp)
	if len(ip) <= 0 {
		panic("ip env is nil")
	}

	name := os.Getenv(tools.PodName)
	if len(name) <= 0 {
		panic("name env is nil")
	}

	room := &Room{
		Handler:     handler,
		Config:      new(game.GameConfig),
		PlayerCount: 0,
		TableCount:  0,
		TableID:     0,
		Ip:          ip,
		Port:        port,
		Name:        name,
		StopCh:      make(chan struct{}),
		WaitClose:   0,
	}
	for _, option := range options {
		option(room)
	}
	if room.RoomCheck == nil {
		room.RoomCheck = &DefaultRoomCheck{}
	}
	return room
}

//InitConfig
func (r *Room) InitConfig(s server.Server, config []byte) error {
	//read config
	if err := json.Unmarshal(config, r.Config); err != nil {
		log.Errorf("init config err %s", err.Error())
		return err
	}

	//create platform client
	r.Platform = platform.NewPlatform(r.Config.Platform)

	//set log level
	if r.Config.Runmode != "dev" {
		log.SetLevel(log.InfoLevel)
	}

	//check room
	go wait.Until(func() {
		r.RoomCheck.CheckRoom(r)
	}, time.Second*5, r.StopCh)

	//log
	log.Infof("init config %v", r.Config)
	return nil
}

//delete game
func (r *Room) Delete(s server.Server, gameid string) error {
	log.Tracef("close this game %s", gameid)
	if atomic.AddInt32(&r.WaitClose, 1) <= 1 {
		//exit check room
		close(r.StopCh)
	}

	//check is all done
	if atomic.LoadInt64(&r.TableCount) <= 0 {
		return nil
	}

	//clsoe all room
	r.Rooms.Range(func(key, value interface{}) bool {
		roomid := key.(uint32)

		//release room
		if _, err := r.Platform.ReleaseRoom(context.Background(), roomid, fmt.Sprintf("%s:%d", r.Ip, r.Port)); err != nil {
			log.Warnf("delete rooms %d server error %s", roomid, err.Error())
			return true
		}

		//delete rooms
		r.Rooms.Delete(roomid)

		//range tables
		r.Tables.Range(func(key, value interface{}) bool {
			table, ok := value.(*tableObject.Table)
			if ok {
				if table.GetRoomID() == roomid {
					table.OnClose()
				}
			}
			return true
		})
		return true
	})

	return errors.New("wait close table")
}

//online
func (r *Room) Online(s server.Server, playerID uint32, tableID int64, conn *websocket.Conn) error {
	online := func() error {
		//get table
		table, ok := r.GetTable(tableID)
		if !ok {
			return errors.New("table id is error !")
		}

		//set
		if err := table.OnOnline(playerID, conn); err != nil {
			log.Errorf("player %d table %d online error %s", playerID, table.GetID(), err.Error())
			return err
		}
		return nil
	}

	if err := online(); err != nil {
		if _, err := r.Platform.PlayerLeaveGame(context.Background(), playerID, false); err != nil {
			log.Warnf("player %d online leave game error %s", playerID, err.Error())
		}
		return err
	}

	//count +1
	atomic.AddInt64(&r.PlayerCount, 1)
	return nil
}

//offline
func (r *Room) Offline(s server.Server, playerID uint32, tableID int64, conn *websocket.Conn) error {
	offline := func() error {
		//get table
		table, ok := r.GetTable(tableID)
		if !ok {
			return errors.New("table id is not found")
		}

		//notic table player leave
		if err := table.OnOffline(playerID, conn); err != nil {
			log.Errorf("player %d table %d offline error %s", playerID, table.GetID(), err.Error())
			return err
		}
		return nil
	}

	if err := offline(); err != nil {
		if _, err := r.Platform.PlayerLeaveGame(context.Background(), playerID, false); err != nil {
			log.Warnf("player %d offline leave game error %s", playerID, err.Error())
		}
		return err
	}

	//count -1
	atomic.AddInt64(&r.PlayerCount, -1)
	return nil
}

//message
func (r *Room) Message(s server.Server, playerID uint32, tableID int64, buff []byte) error {
	//get table
	table, ok := r.GetTable(tableID)
	if !ok {
		return errors.New("table id is not found")
	}

	//table on message
	if err := table.OnMessage(playerID, buff); err != nil {
		log.Errorf("player %d table %d message error %s", playerID, table.GetID(), err.Error())
		return err
	}
	return nil
}

//get table by id
func (r *Room) GetTable(id int64) (*tableObject.Table, bool) {
	value, ok := r.Tables.Load(id)
	if !ok {
		return nil, false
	}
	return value.(*tableObject.Table), true
}

//wait func
func (r *Room) StartAndWaitTable(table *tableObject.Table) {
	atomic.AddInt64(&r.TableCount, 1)
	go func() {
		defer atomic.AddInt64(&r.TableCount, -1)
		r.Handler.InitTable(table)
		r.Tables.Delete(table.GetID())
	}()
}

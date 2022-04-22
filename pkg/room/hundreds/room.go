package room

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	modelRoom "github.com/kubegames/kubegames-hall/app/model/room"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	playerObject "github.com/kubegames/kubegames-sdk/pkg/player"
	"github.com/kubegames/kubegames-sdk/pkg/room"
	"github.com/kubegames/kubegames-sdk/pkg/server"
	tableObject "github.com/kubegames/kubegames-sdk/pkg/table"
)

//room check
type RoomCheck struct {
}

//check room
func (d *RoomCheck) CheckRoom(r *room.Room) {
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
func (d *RoomCheck) OpenRoom(r *room.Room, room *modelRoom.Room) {
	if int64(r.Config.MaxPeople) <= int64(room.MaxPeople)*atomic.LoadInt64(&r.TableCount) {
		return
	}

	if _, err := r.Platform.AcquireRoom(context.Background(), room.RoomID, fmt.Sprintf("%s:%d", r.Ip, r.Port)); err != nil {
		return
	}

	if _, ok := r.Rooms.Load(room.RoomID); ok {
		return
	}

	//not allow close
	room.IsAllowClose = false

	//not allow create table
	room.IsAllowAutoCreateTable = false

	//new table
	table := tableObject.NewTable(r.Platform, atomic.AddInt64(&r.TableID, 1), 0, room)

	//start
	r.StartAndWaitTable(table)

	//store table
	r.Tables.Store(table.GetID(), table)

	//store
	r.Rooms.Store(room.RoomID, room)

	log.Infof("open roomid %d config %s", room.RoomID, room)
	return
}

//close room
func (d *RoomCheck) CloseRoom(r *room.Room, roomid uint32) {
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

type Room struct {
	*room.Room
	Server server.Server
}

//run
func (r *Room) Run() {
	log.Infof("kubegames run server %s:%d", r.Ip, r.Port)
	if err := r.Server.Run(r.Ip, r.Port); err != nil {
		panic(err.Error())
	}
}

//new room
func NewRoom(handler room.RoomHandler) *Room {
	room := &Room{
		Room: room.NewRoom(handler, room.WithOptionRoomCheck(&RoomCheck{})),
	}
	room.Server = server.NewServer(room, room.Name)
	return room
}

//match
func (r *Room) Match(s server.Server, player *playerObject.Player) (int64, bool, error) {
	log.Tracef("match player %d to room %d", player.PlayerID, player.RoomID)

	//check that the maximum number of people is reached
	if atomic.LoadInt64(&r.PlayerCount) >= int64(r.Config.MaxPeople) {
		log.Warnln("the number of people online has been reached")
		return 0, false, errors.New("the number of people online has been reached")
	}

	//check game id
	if player.GameID != r.Config.GameID {
		return 0, false, errors.New("the game id is incorrect")
	}

	//check room
	value, ok := r.Rooms.Load(player.RoomID)
	if !ok {
		return 0, false, errors.New("the room id is incorrect")
	}
	room, ok := value.(*modelRoom.Room)
	if !ok {
		log.Warnln("sync map room to *room fail")
		return 0, false, errors.New("the room is incorrect")
	}

	//if is open cross plat form match
	if room.PlatformID != player.PlatformID {
		if room.IsOpenCrossPlatformMatch == false {
			return 0, false, errors.New("the platform is incorrect")
		}

		ok := false
		for _, platform := range room.AllowPlatformID {
			if platform == player.PlatformID {
				ok = true
				break
			}
		}

		if !ok {
			log.Warnf("cross platform matching fail player platform %d room allowplatformid %v", player.PlatformID, room.AllowPlatformID)
			return 0, false, errors.New("the platform is incorrect")
		}
	}

	//your credit is running low
	if player.Balance <= int64(room.EntranceRestrictions) {
		return 0, false, errors.New("sorry, your credit is running low")
	}

	//default match fail
	stop := false
	success := false
	var tableID int64 = 0

	//range match
	r.Tables.Range(func(key, value interface{}) bool {
		table, ok := value.(*tableObject.Table)
		if ok {
			if table.GetRoomID() == player.RoomID {
				log.Tracef("match player %d table %d", player.PlayerID, table.GetID())

				isStop, err := table.OnMatch(player)
				if err != nil {
					if isStop == true {
						stop = true
						success = false
						return false
					}
					return true
				}

				//match success
				stop = true
				success = true
				tableID = table.GetID()
				return false
			}
		}
		return true
	})

	//match success
	if success {
		return tableID, stop, nil
	}

	//match player fail
	return 0, stop, errors.New("match error")
}

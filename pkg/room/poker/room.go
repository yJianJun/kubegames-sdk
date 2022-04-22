package room

import (
	"errors"
	"sync/atomic"

	modelRoom "github.com/kubegames/kubegames-hall/app/model/room"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	playerObject "github.com/kubegames/kubegames-sdk/pkg/player"
	"github.com/kubegames/kubegames-sdk/pkg/room"
	"github.com/kubegames/kubegames-sdk/pkg/server"
	tableObject "github.com/kubegames/kubegames-sdk/pkg/table"
)

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
		Room: room.NewRoom(handler),
	}
	room.Server = server.NewServer(room, room.Name)
	return room
}

//match
func (r *Room) Match(s server.Server, player *playerObject.Player) (int64, bool, error) {
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

	if room.MaxPeople > 1 {
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
	}

	//check whether to create table
	if room.IsAllowAutoCreateTable == true && atomic.LoadInt64(&r.TableCount)*int64(room.MaxPeople) < int64(r.Config.MaxPeople) {

		//new table
		table := tableObject.NewTable(r.Platform, atomic.AddInt64(&r.TableID, 1), 0, room)

		//start
		r.StartAndWaitTable(table)

		//start match
		isStop, err := table.OnMatch(player)
		if err != nil {
			table.OnClose()
			return 0, isStop, err
		}

		//store table
		r.Tables.Store(table.GetID(), table)

		log.Tracef("match player %d  create table %d", player.PlayerID, table.GetID())
		return table.GetID(), true, nil
	}

	//match player fail
	return 0, stop, errors.New("match error")
}

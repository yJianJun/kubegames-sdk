package player

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	playerModel "github.com/kubegames/kubegames-hall/app/model/player"
	"github.com/kubegames/kubegames-sdk/app/message"
	appMessage "github.com/kubegames/kubegames-sdk/app/message"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	"github.com/sony/sonyflake"
	"k8s.io/client-go/util/workqueue"
)

type Player struct {
	//玩家信息
	*playerModel.Player
	//游戏 ID
	GameID uint32
	//房间 ID
	RoomID uint32
	//玩家使用 token
	Token string
	//websocket 链接句柄
	Conn *websocket.Conn
	//是否在线
	Online bool
	//桌子 ID
	TableID int64
	//Robot
	Robot *Robot
	//Chair
	Chair int
	//变动金额
	ChangeBalance int64
	//lock
	lock sync.RWMutex
}

//load config
func NewPlayer(gameID uint32, roomID uint32, token string, player *playerModel.Player, robot bool, server Server) (*Player, error) {
	log.Infof("new player %d balance %d chip %d", player.PlayerID, player.Balance, player.Chip)

	p := &Player{
		Player:        player,
		GameID:        gameID,
		RoomID:        roomID,
		Token:         token,
		Online:        false,
		Conn:          nil,
		TableID:       0,
		Chair:         0,
		Robot:         nil,
		ChangeBalance: 0,
	}

	if robot == true {
		p.Player.IsRobot = true
		p.Robot = NewRobot(p, server)
	}
	return p, nil
}

//bind robot handler
func (p *Player) BindRobotHandler(handler RobotHandler) {
	if p.Robot == nil {
		return
	}
	p.Robot.BindHandler(handler)
}

//online
func (p *Player) IsOnline() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.Online
}

//online
func (p *Player) SetOnline(conn *websocket.Conn) {
	p.lock.Lock()
	if p.Conn != nil && p.Conn != conn {
		p.Conn.Close()
	}
	p.Conn = conn
	if p.Robot != nil {
		p.Robot.queue = workqueue.NewDelayingQueue()

		//start
		go p.Robot.Start()
	}
	p.Online = true
	p.lock.Unlock()
}

//offline
func (p *Player) SetOffline() {
	p.lock.Lock()
	if p.Conn != nil {
		p.Conn.Close()
		p.Conn = nil
	}
	if p.Robot != nil {
		p.Robot.OnClose()
		p.Robot = nil
	}
	p.Online = false
	p.lock.Unlock()
}

//check conn
func (p *Player) CheckConn(conn *websocket.Conn) bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if p.Conn != conn {
		return false
	}
	return true
}

//get table id
func (p *Player) GetTableID() int64 {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.TableID
}

//send msg to robot
func (p *Player) SendRobotMsg(mainCmd int32, subCmd int32, buff []byte) error {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.Robot != nil {
		if err := p.Robot.OnMessage(mainCmd, subCmd, buff); err != nil {
			log.Warnf("send robot message %s", err.Error())
			return err
		}
		return nil
	}
	return fmt.Errorf("robot %d is nil", p.PlayerID)
}

//send msg to robot
func (p *Player) SendPlayerMsg(mainCmd int32, subCmd int32, buff []byte) error {
	p.lock.RLock()
	defer p.lock.RUnlock()

	//check conn
	if p.Conn == nil {
		return errors.New("conn is close")
	}

	//proto
	message, err := proto.Marshal(&message.FrameMsg{MainCmd: mainCmd, SubCmd: subCmd, Buff: buff, Time: time.Now().Unix()})
	if err != nil {
		log.Warnln(err.Error())
		return err
	}

	//write
	if err := p.Conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
		log.Warnf("send message %s", err.Error())
		return err
	}
	return nil
}

//send main message
func (p *Player) SendMainMsg(subCmd int32, pb proto.Message) (err error) {
	//online
	if p.IsOnline() == false {
		return fmt.Errorf("robot %d is offline", p.PlayerID)
	}

	var buff []byte
	if pb != nil {
		//protobuf marshal
		buff, err = proto.Marshal(pb)
		if err != nil {
			log.Warnln(err.Error())
			return err
		}
	}

	//send robot
	if p.IsRobot() {
		if err := p.SendRobotMsg(int32(message.MSGTYPE_MAINCMD), subCmd, buff); err != nil {
			p.SetOffline()
			log.Warnf("send robot %d message %s", p.GetID(), err.Error())
			return err
		}
		return nil
	}

	//send player msg
	if err := p.SendPlayerMsg(int32(message.MSGTYPE_MAINCMD), subCmd, buff); err != nil {
		p.SetOffline()
		log.Warnf("send player %d message %s", p.GetID(), err.Error())
		return err
	}
	return nil
}

//set chair
func (p *Player) SetChairID(chair int) {
	p.Chair = chair
}

type PlayerInterface interface {
	//WriteLogs 发送日志
	SendLogs(gameNum string, records []*appMessage.GameLog)

	//生成局号
	GetRoomNum() string

	//获取ip
	GetIP() string

	//获取城市
	GetCity() string

	//获取用户性别
	GetSex() int32

	//创建跑马灯，content 跑马灯内容
	//CreateMarquee(content string) error

	//上庄
	UpBanker()

	//下庄
	DownBanker()

	//发送打码量
	SendChip(chip int64)

	//设置结算牌
	SetEndCards(cards string)

	//获取点控级别
	GetProb() int32

	/*
	*获取用户昵称
	*返回值说明:
	*@return:string   		昵称
	 */
	GetNike() string

	/*
	*获取用户头像
	*返回值说明:
	*@return:string   		用户头像
	 */
	GetHead() string

	/*
	*获取用户ID
	*返回值说明:
	*@return:int   		用户ID
	 */
	GetID() int64

	/*
	*获取用户积分
	*返回值说明:
	*@return:int   		积分
	 */
	GetScore() int64

	/*
		*获取用户积分
		*参数说明:
		*@param:gameNum		局号
		*@param:score		积分
		*@param:tax			税收（万分比）
		*返回值
		@return int64 收取的税收
		@return int64 扣完税的钱
	*/
	SetScore(gameNum string, score int64, tax int64) (int64, int64)

	/*
	*获取椅子ID
	*返回值说明:
	*@return:id   		椅子ID
	 */
	GetChairID() int

	/*
	*设置用户数据
	*参数说明:
	*@param:data		用户数据
	 */
	SetTableData(data string)

	/*
	*获取用户数据
	*返回值说明:
	*@return:data   		用户数据
	 */
	GetTableData() string

	/*
	*删除用户数据
	*返回值说明:
	 */
	DelTableData()

	/*
	*用户发送消息
	*参数说明:
	*@param:subCmd		消息类型
	*@param:pb		消息
	*返回值说明:
	*@return:error   	错误
	 */
	SendMsg(subCmd int32, pb proto.Message) error

	/*
	*判断用户是否为机器人--逻辑
	*参数说明:
	*@param:bool	true是机器人 false不是机器人
	 */
	IsRobot() bool
}

//WriteLogs 发送日志
func (p *Player) SendLogs(gameNum string, records []*appMessage.GameLog) {
	//log.Tracef("send player %d logs gamenum %s records %v", p.PlayerID, gameNum, records)
}

//绑定机器人逻辑
//BindRobot(interRobot RobotInter) AIUserInter

//GetUID 获取UID
// func (p *Player) GetUID() int64 {
// }

//生成局号
func (p *Player) GetRoomNum() string {
	flake := sonyflake.NewSonyflake(sonyflake.Settings{})
	id, err := flake.NextID()
	if err != nil {
		log.Errorf(err.Error())
		return ""
	}
	return fmt.Sprintf("%x", id)
}

//获取ip
func (p *Player) GetIP() string {
	return p.Ip
}

//获取城市
func (p *Player) GetCity() string {
	return p.City
}

//获取用户性别
func (p *Player) GetSex() int32 {
	return p.Sex
}

//创建跑马灯，content 跑马灯内容
//CreateMarquee(content string) error

//上庄
func (p *Player) UpBanker() {
	log.Tracef("player %d up banker", p.PlayerID)
}

//下庄
func (p *Player) DownBanker() {
	log.Tracef("player %d down banker", p.PlayerID)
}

//发送打码量
func (p *Player) SendChip(chip int64) {
	if chip <= 0 {
		return
	}
	p.Chip = p.Chip + chip
}

//设置结算牌
func (p *Player) SetEndCards(cards string) {
	//log.Tracef("set end cards %s", cards)
}

//获取点控级别
func (p *Player) GetProb() int32 {
	return p.Pointctl
}

/*
*获取用户昵称
*返回值说明:
*@return:string   		昵称
 */
func (p *Player) GetNike() string {
	return p.Nick
}

/*
*获取用户头像
*返回值说明:
*@return:string   		用户头像
 */
func (p *Player) GetHead() string {
	return p.Avatar
}

/*
*获取用户ID
*返回值说明:
*@return:int   		用户ID
 */
func (p *Player) GetID() int64 {
	return int64(p.PlayerID)
}

/*
*获取用户积分
*返回值说明:
*@return:int   		积分
 */
func (p *Player) GetScore() int64 {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.Balance
}

/*
	*获取用户积分
	*参数说明:
	*@param:gameNum		局号
	*@param:score		积分
	*@param:tax			税收（万分比）
	*返回值
	@return int64 收取的税收
	@return int64 扣完税的钱
*/
func (p *Player) SetScore(gameNum string, score int64, tax int64) (int64, int64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	before := p.Balance
	taxMoney := int64(0)
	//money := score
	profiltAmount := score
	if score > 0 {
		if profiltAmount > 0 {
			taxMoney = ((score) * tax) / 10000
			y := ((score) * tax) % 10000
			if y > 0 {
				taxMoney = taxMoney + 1
			}
		} else {
			profiltAmount = 0
		}
	}
	score = score - taxMoney
	after := before + score

	//change balance
	p.ChangeBalance = p.ChangeBalance + score

	//set balance
	p.Balance = after
	return taxMoney, score
}

/*
*获取椅子ID
*返回值说明:
*@return:id   		椅子ID
 */
func (p *Player) GetChairID() int {
	return p.Chair
}

/*
*设置用户数据
*参数说明:
*@param:data		用户数据
 */
func (p *Player) SetTableData(data string) {
	//log.Tracef("set player %d table data %s", p.PlayerID, data)
}

/*
*获取用户数据
*返回值说明:
*@return:data   		用户数据
 */
func (p *Player) GetTableData() string {
	//log.Tracef("get player %d table data", p.PlayerID)
	return ""
}

/*
*删除用户数据
*返回值说明:
 */
func (p *Player) DelTableData() {
	//log.Tracef("delete player %d table data", p.PlayerID)
}

/*
*用户发送消息
*参数说明:
*@param:subCmd		消息类型
*@param:pb		消息
*返回值说明:
*@return:error   	错误
 */
func (p *Player) SendMsg(subCmd int32, pb proto.Message) (err error) {
	//online
	if p.IsOnline() == false {
		return fmt.Errorf("robot %d is offline", p.PlayerID)
	}

	var buff []byte
	if pb != nil {
		//protobuf marshal
		buff, err = proto.Marshal(pb)
		if err != nil {
			log.Warnln(err.Error())
			return err
		}
	}

	//send robot
	if p.IsRobot() == true {
		if err := p.SendRobotMsg(int32(message.MSGTYPE_SUBCMD), subCmd, buff); err != nil {
			p.SetOffline()
			log.Warnf("send robot %d message %s", p.GetID(), err.Error())
			return err
		}
		return nil
	}

	//send player msg
	if err := p.SendPlayerMsg(int32(message.MSGTYPE_SUBCMD), subCmd, buff); err != nil {
		p.SetOffline()
		log.Warnf("send player %d message %s", p.GetID(), err.Error())
		return err
	}
	return nil
}

/*
*判断用户是否为机器人--逻辑
*参数说明:
*@param:bool	true是机器人 false不是机器人
 */
func (p *Player) IsRobot() bool {
	return p.Player.IsRobot
}

package table

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"github.com/kubegames/kubegames-hall/app/model/room"
	"github.com/kubegames/kubegames-hall/app/service/platform/types"
	"github.com/kubegames/kubegames-sdk/app/message"
	appMessage "github.com/kubegames/kubegames-sdk/app/message"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	"github.com/kubegames/kubegames-sdk/pkg/platform"
	"github.com/kubegames/kubegames-sdk/pkg/player"
	playerObject "github.com/kubegames/kubegames-sdk/pkg/player"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
)

//set rand
func init() {
	rand.Seed(time.Now().UnixNano())
}

//match kind
type MatchKind int

//错误类型
const (
	//坐下success
	SitDownOk MatchKind = 1
	//坐下错误 常规 继续往下匹配
	SitDownErrorNomal MatchKind = 2
	//坐下错误 不常规 不继续匹配
	SitDownErrorOver MatchKind = 3
)

//TableHandler 游戏逻辑
type TableHandler interface {
	//用户坐下（进入table）
	OnActionUserSitDown(player player.PlayerInterface, chairID int, config string) MatchKind

	//游戏逻辑消息
	OnGameMessage(subCmd int32, buffer []byte, player player.PlayerInterface)

	//绑定机器人逻辑
	BindRobot(robot player.RobotInterface) player.RobotHandler

	//发送场景消息---用户匹配success，通知逻辑发送场景消息
	SendScene(player player.PlayerInterface)

	//用户准备
	UserReady(player player.PlayerInterface) bool

	//用户准备success后，通知逻辑开始正常游戏
	GameStart()

	//用户离开游戏
	UserLeaveGame(player player.PlayerInterface) bool

	//用户掉线
	UserOffline(player player.PlayerInterface) bool

	//关闭桌子
	CloseTable()
}

//job kind
type Kind int

const (
	Message Kind = 1
	Timer   Kind = 2
	Match   Kind = 3
	Close   Kind = 4
	Online  Kind = 7
	Offline Kind = 6
)

//match result
type result struct {
	isStop  bool
	success bool
	err     error
}

//job
type Job struct {
	//id
	id uint64
	//player id
	playerID uint32
	//conn
	conn *websocket.Conn
	//kind
	kind Kind
	//player
	player *playerObject.Player
	//body
	body []byte
	//count
	count uint64
	//param
	param interface{}
	//interval
	interval time.Duration
	//runTime
	executionTime time.Time
	//timer func
	f func(param interface{})
	//match
	result chan *result
}

//GetTimeDifference 获取距离执行时间相差多少
func (j *Job) GetTimeDifference() int64 {
	if j.executionTime.IsZero() == true {
		return 0
	}
	return (j.executionTime.UnixNano() - time.Now().UnixNano()) / 1e6
}

type TableInterface interface {
	//开启桌子
	Start(handler TableHandler, before func(), after func())

	//关闭桌子
	Close()

	//桌子玩家数量
	PlayerCount() int

	//获取桌子编号
	GetID() int64

	//获取等级
	GetLevel() int32

	//获取房间税收
	GetRoomRate() int64

	//获取局号
	GetGameNum() string

	//获取房间id
	GetRoomID() uint32

	//获取房间配置
	GetConfig() *room.Room

	//获取入场限制
	GetEntranceRestrictions() int64

	//获取平台id
	GetPlatformID() int64

	//获取自定义配置
	GetAdviceConfig() string

	//获取血池
	GetRoomProb() int32

	//获取机器人
	GetRobot(number uint32, minBalance, maxBalance int64) error

	//桌子是否需要关闭
	IsClose() bool

	//写日志,userId 0 系统日志 其他为用户日志
	WriteLogs(userID int64, content string)

	//上传战绩
	//playerID 玩家ID
	//gameNum 局号
	//profitAmount 盈利
	//betsAmount 总下注
	//drawAmount 总抽水
	//outputAmount 总产出
	//endCards 结算牌
	UploadPlayerRecord(records []*platform.PlayerRecord) (bool, error)

	//获取跑马灯配置
	GetMarqueeConfig() (configs []*appMessage.MarqueeConfig)

	//创建跑马灯， nickName 昵称, gold 金额, special 特殊条件
	CreateMarquee(nickName string, gold int64, special string, ruleID int64) error

	//获取奖金池 ration	万分比 userId	用户id int64:总共的值 int64:实际赢的数量 error 错误
	GetCollect(ration int32, userID int64) (int64, int64, error)

	//开始游戏
	StartGame()

	//结束游戏
	EndGame()

	//提出用户
	KickOut(player player.PlayerInterface)

	//广播
	Broadcast(subCmd int32, pb proto.Message)

	//给大厅发送消息
	SendToHall(subCmd int32, pb proto.Message) error

	//AddTimer 添加一次性定时器
	AddTimer(interval int64, jobFunc func()) (job *Job, ok bool)

	//AddTimerParam 添加带参数的一次性定时器
	AddTimerParam(actionInterval int64, jobFunc func(interface{}), param interface{}) (job *Job, ok bool)

	//AddTimerRepeat 添加多次定时器
	AddTimerRepeat(interval int64, num uint64, jobFunc func()) (job *Job, inserted bool)

	//AddTicker 添加一个循环定时器
	AddTicker(interval int64, jobFunc func()) (job *Job, inserted bool)

	//delete job
	DeleteJob(job *Job)
}

type Table struct {
	//tableID
	id int64
	//room配置
	config *room.Room
	//playes
	players map[uint32]*player.Player
	//match
	matchplayers map[uint32]*player.Player
	//机器人数量
	robotCount int32
	//局id
	gameNum string
	//血池
	roomProb int32
	//table逻辑处理
	handler TableHandler
	//jobs
	jobs sync.Map
	//job id
	jobid uint64
	//job pool
	jobPool sync.Pool
	//queue
	queue workqueue.DelayingInterface
	//stopCh chan struct{}
	stopCh chan struct{}
	//isclose
	isclose bool
	//chairs
	chairs map[int]int
	//start
	start bool
	//platform client
	platform platform.Platform
}

//load config
func NewTable(platform platform.Platform, id int64, roomProb int32, config *room.Room) *Table {
	t := &Table{
		id:           id,
		config:       config,
		robotCount:   0,
		roomProb:     roomProb,
		players:      make(map[uint32]*playerObject.Player),
		matchplayers: make(map[uint32]*playerObject.Player),
		stopCh:       make(chan struct{}),
		platform:     platform,
		isclose:      false,
		queue:        workqueue.NewDelayingQueue(),
		chairs:       map[int]int{},
		jobPool: sync.Pool{
			New: func() interface{} {
				return &Job{}
			},
		},
	}

	//set chairt
	for n := 0; n < int(t.config.MaxPeople); n++ {
		t.chairs[n] = n
	}
	return t
}

//start
func (t *Table) Start(handler TableHandler, before func(), after func()) {
	log.Infof("start room[%d] config[%v] tableid[%d]", t.config.RoomID, t.config, t.id)
	//set handler
	t.handler = handler

	if before != nil {
		before()
	}

	//wait
	wait.Until(t.runWorker, time.Millisecond, t.stopCh)

	//log
	log.Infof("start return room[%d] config[%v] tableid[%d]", t.config.RoomID, t.config, t.id)

	//delete
	t.jobs.Range(func(key, value interface{}) bool {
		t.DeleteJob(value.(*Job))
		return true
	})

	if after != nil {
		after()
	}
	//log
	log.Infof("exit room[%d] config[%v] tableid[%d]", t.config.RoomID, t.config, t.id)
}

//close table
func (t *Table) Close() {
	t.queue.ShutDown()
	log.Tracef("close table[%d] platform[%d] room[%d] players %d match %d", t.id, t.config.PlatformID, t.config.RoomID, len(t.players), len(t.matchplayers))
}

//桌子是否需要关闭
func (t *Table) IsClose() bool {
	return t.isclose
}

//桌子玩家数量
func (t *Table) PlayerCount() int {
	return len(t.players) + len(t.matchplayers)
}

//get id
func (t *Table) GetID() int64 {
	return t.id
}

//get room id
func (t *Table) GetRoomID() uint32 {
	return t.config.RoomID
}

//get room config
func (t *Table) GetConfig() *room.Room {
	return t.config
}

//写日志,userId 0 系统日志 其他为用户日志
func (t *Table) WriteLogs(userID int64, content string) {
	//log.Tracef("write logs player %d content %s", userID, content)
}

//上传战绩
//playerID 玩家ID
//gameNum 局号
//profitAmount 盈利
//betsAmount 总下注
//drawAmount 总抽水
//outputAmount 总产出
//endCards 结算牌
func (t *Table) UploadPlayerRecord(records []*platform.PlayerRecord) (bool, error) {
	if len(records) > 0 {
		return t.platform.UploadPlayerRecord(context.Background(), t.config.GameID, t.config.RoomID, records)
	}
	return true, nil
}

//获取跑马灯配置
func (t *Table) GetMarqueeConfig() (configs []*appMessage.MarqueeConfig) {
	return
}

//创建跑马灯， nickName 昵称, gold 金额, special 特殊条件
func (t *Table) CreateMarquee(nickName string, gold int64, special string, ruleID int64) error {
	//log.Tracef("Create Marquee nickName[%s] gold[%d] special[%s] ruleID[%d]", nickName, gold, special, ruleID)
	return nil
}

/*
*获取奖金池
*参数说明:
*@param:ration	万分比
*@param:userId	用户id
*返回值
*第一个 int64:总共的值
*第二个 int64:实际赢的数量
*error 错误
 */
func (t *Table) GetCollect(ration int32, userID int64) (int64, int64, error) {
	return 0, 0, nil
}

//获取等级
func (t *Table) GetLevel() int32 {
	return t.config.Level
}

//获取房间税收
func (t *Table) GetRoomRate() int64 {
	return int64(t.config.Rate)
}

//获取局号
func (t *Table) GetGameNum() string {
	return t.gameNum
}

//获取入场限制 -1为没有初始化入场限制
func (t *Table) GetEntranceRestrictions() int64 {
	return int64(t.config.EntranceRestrictions)
}

//获取平台id -1为没有平台id
func (t *Table) GetPlatformID() int64 {
	return int64(t.config.PlatformID)
}

//获取自定义配置
func (t *Table) GetAdviceConfig() string {
	return t.config.AdviceConfig
}

//获取血池 3000, 2000, 1000, -1000, -2000, -3000
func (t *Table) GetRoomProb() int32 {
	return t.roomProb
}

//给大厅发送消息
func (t *Table) SendToHall(subCmd int32, pb proto.Message) error {
	//protobuf marshal
	buff, err := proto.Marshal(pb)
	if err != nil {
		log.Warnf("send to platform sub proto marshal error %s", err.Error())
		return err
	}

	//proto
	message, err := proto.Marshal(&message.FrameMsg{MainCmd: int32(message.MSGTYPE_SUBCMD), SubCmd: subCmd, Buff: buff, Time: time.Now().Unix()})
	if err != nil {
		log.Warnf("send to platform main proto marshal error %s", err.Error())
		return err
	}

	if _, err := t.platform.Broadcast(context.Background(), message); err != nil {
		log.Warnf("send to platform error %s", err.Error())
		return err
	}
	return nil
}

//获取机器人
func (t *Table) GetRobot(number uint32, minBalance, maxBalance int64) error {
	//check queue is close
	if t.queue.ShuttingDown() || t.isclose == true {
		return errors.New("the table is closing")
	}

	if t.config.IsOpenAiRobot == false {
		return errors.New("the table is not open robot")
	}

	//check people num
	if t.config.MaxPeople <= int32(len(t.players)+len(t.matchplayers)) {
		return errors.New("players have been maximized")
	}

	//check chair
	if len(t.chairs) <= 0 {
		return errors.New("no chair assignment")
	}

	//get robot
	rsp, err := t.platform.ApplyRobot(context.Background(), &types.ApplyRobotRequest{
		GameID:               t.config.GameID,
		RoomID:               t.config.RoomID,
		Number:               number,
		Entrancerestrictions: int64(t.config.EntranceRestrictions),
		MaxBalance:           maxBalance,
		MinBalance:           minBalance,
	})
	if err != nil {
		log.Errorf("get robot error %s", err.Error())
		return err
	}

	for _, r := range rsp.List {
		//mock player
		robot, err := player.NewPlayer(rsp.GameID, rsp.RoomID, r.Token, r.Player, true, t)
		if err != nil {
			log.Errorf("new player error %s", err.Error())
			//delete robot match
			if _, err := t.platform.PlayerLeaveGame(context.Background(), robot.PlayerID, true); err != nil {
				log.Warnf("player %d leave game error %s", robot.PlayerID, err.Error())
			}
			return err
		}

		//set chair
		for vli := range t.chairs {
			robot.SetChairID(vli)
			break
		}

		//user sit down
		if t.handler.OnActionUserSitDown(robot, robot.GetChairID(), t.config.AdviceConfig) != SitDownOk {
			//delete robot
			if _, err := t.platform.PlayerLeaveGame(context.Background(), robot.PlayerID, true); err != nil {
				log.Warnf("player %d leave game error %s", robot.PlayerID, err.Error())
			}
			return errors.New("logic does not allow you to sit down ")
		}

		//delete chair
		delete(t.chairs, robot.GetChairID())

		//set conn
		robot.SetOnline(nil)

		//delete
		delete(t.matchplayers, robot.PlayerID)

		//set player
		t.players[robot.PlayerID] = robot

		//bind robot handler
		robot.BindRobotHandler(t.handler.BindRobot(robot.Robot))

		//send scene
		t.handler.SendScene(robot)

		//send player enter
		if err := robot.SendMainMsg(int32(appMessage.MSGKIND_MATCH), &appMessage.S2CEnter{
			UserId:   int64(robot.PlayerID), //playerID
			Head:     robot.Avatar,          //头像url
			Gold:     robot.Balance,         //player金币
			NickName: robot.Nick,            //player昵称
			Sign:     robot.Sign,            //player签名
			TableId:  int32(t.id),           //table号
			SetId:    int32(1),              //椅子号
		},
		); err != nil {
			log.Errorf("robot %d send online msg is error %s", robot.PlayerID, err.Error())
		}

		//log
		log.Tracef("robot %d is online", robot.PlayerID)
	}

	return nil
}

//开始游戏
func (t *Table) StartGame() {
	//3000, 2000, 1000, -1000, -2000, -3000
	if value, err := t.platform.GetRoomPool(context.Background(), t.config.RoomID); err != nil {
		log.Errorf("get room %d prob error %s", t.config.RoomID, err.Error())
		t.roomProb = 0
	} else {
		if value > 0 && value <= 100000 {
			t.roomProb = 1000
		}
		if value > 100000 && value <= 200000 {
			t.roomProb = 2000
		}
		if value > 200000 {
			t.roomProb = 3000
		}
		if value <= 0 && value > -100000 {
			t.roomProb = 0
		}
		if value <= -100000 && value > -200000 {
			t.roomProb = -1000
		}
		if value <= -200000 && value > -300000 {
			t.roomProb = -2000
		}
		if value <= -300000 {
			t.roomProb = -3000
		}
	}

	//game start
	t.start = true

	//check robot number
	if t.isclose == false {
		t.checkRobot()
	}

	//create games num
	t.gameNum = fmt.Sprintf("%d%d", time.Now().Unix(), t.id)

	log.Infof("game platform[%d] room[%d] table[%d] gameNum[%s] is start", t.config.PlatformID, t.config.RoomID, t.id, t.gameNum)
}

//结束游戏
func (t *Table) EndGame() {
	//game start
	t.start = false

	//range offline
	for _, player := range t.players {
		if player.IsOnline() == false {

			//user exit
			if t.handler.UserOffline(player) == false {
				log.Warnf("logic does not agree with the player %d to leave", player.PlayerID)
				continue
			}

			//leave player
			t.playerLeave(player)
		}
	}

	//log
	log.Infof("game platform[%d] room[%d] table[%d] is end", t.config.PlatformID, t.config.RoomID, t.id)
}

//提出用户
func (t *Table) KickOut(player player.PlayerInterface) {
	//get player
	p, ok := t.players[uint32(player.GetID())]
	if ok {
		//set leave
		t.playerLeave(p)

		//log
		log.Tracef("kick out player %d", player.GetID())
	}
}

//广播
func (t *Table) Broadcast(subCmd int32, pb proto.Message) {
	for _, player := range t.players {
		player.SendMsg(subCmd, pb)
	}
}

//添加一次性定时器
func (t *Table) AddTimer(interval int64, jobFunc func()) (job *Job, ok bool) {
	return t.OnTimer(time.Millisecond*time.Duration(interval), 1, func(param interface{}) {
		jobFunc()
	}, nil)
}

//添加带参数的一次性定时器
func (t *Table) AddTimerParam(actionInterval int64, jobFunc func(interface{}), param interface{}) (job *Job, ok bool) {
	return t.OnTimer(time.Millisecond*time.Duration(actionInterval), 1, jobFunc, param)
}

//添加多次定时器
func (t *Table) AddTimerRepeat(interval int64, num uint64, jobFunc func()) (job *Job, inserted bool) {
	return t.OnTimer(time.Millisecond*time.Duration(interval), num, func(param interface{}) {
		jobFunc()
	}, nil)
}

//添加一个循环定时器
func (t *Table) AddTicker(interval int64, jobFunc func()) (job *Job, inserted bool) {
	return t.OnTimer(time.Millisecond*time.Duration(interval), 0, func(param interface{}) {
		jobFunc()
	}, nil)
}

//on match
func (t *Table) OnMatch(player *playerObject.Player) (bool, error) {
	if t.isclose {
		return false, errors.New("is close")
	}
	if t.queue.ShuttingDown() {
		return false, errors.New("queue is shutting")
	}

	job, ok := t.jobPool.Get().(*Job)
	if !ok {
		return false, errors.New("get job fail")
	}
	job.kind = Match
	job.player = player
	job.id = atomic.AddUint64(&t.jobid, 1)
	job.result = make(chan *result)
	t.jobs.Store(job.id, job)
	t.queue.Add(job.id)

	//wait
	result := <-job.result
	return result.isStop, result.err
}

//on online
func (t *Table) OnOnline(playerID uint32, conn *websocket.Conn) error {
	if t.queue.ShuttingDown() {
		return errors.New("queue is shutting")
	}

	//get job
	job, ok := t.jobPool.Get().(*Job)
	if !ok {
		return errors.New("get job fail")
	}
	job.kind = Online
	job.playerID = playerID
	job.conn = conn
	job.id = atomic.AddUint64(&t.jobid, 1)
	t.jobs.Store(job.id, job)
	t.queue.Add(job.id)
	return nil
}

//on message
func (t *Table) OnMessage(playerID uint32, buff []byte) error {
	if t.queue.ShuttingDown() {
		return errors.New("queue is shutting")
	}

	//get job
	job, ok := t.jobPool.Get().(*Job)
	if !ok {
		return errors.New("get job fail")
	}
	job.kind = Message
	job.body = buff
	job.playerID = playerID
	job.id = atomic.AddUint64(&t.jobid, 1)
	t.jobs.Store(job.id, job)
	t.queue.Add(job.id)
	return nil
}

//on message
func (t *Table) OnClose() error {
	if t.queue.ShuttingDown() {
		return errors.New("queue is shutting")
	}

	//get job
	job, ok := t.jobPool.Get().(*Job)
	if !ok {
		return errors.New("get job fail")
	}
	job.kind = Close
	job.id = atomic.AddUint64(&t.jobid, 1)
	t.jobs.Store(job.id, job)
	t.queue.Add(job.id)
	return nil
}

//on offline
func (t *Table) OnOffline(playerID uint32, conn *websocket.Conn) error {
	if t.queue.ShuttingDown() {
		return errors.New("queue is shutting")
	}

	//get job
	job, ok := t.jobPool.Get().(*Job)
	if !ok {
		return errors.New("get job fail")
	}
	job.kind = Offline
	job.playerID = playerID
	job.conn = conn
	job.id = atomic.AddUint64(&t.jobid, 1)
	t.jobs.Store(job.id, job)
	t.queue.Add(job.id)
	return nil
}

//on timer
func (t *Table) OnTimer(interval time.Duration, count uint64, f func(param interface{}), param interface{}) (*Job, bool) {
	if t.queue.ShuttingDown() {
		log.Warnf("table %d queue is shutting down !!!", t.id)
		return nil, false
	}
	job, ok := t.jobPool.Get().(*Job)
	if !ok {
		log.Warnf("table %d job pool get fail !!!", t.id)
		return nil, false
	}
	job.count = count
	job.kind = Timer
	job.f = f
	job.interval = interval
	job.param = param
	job.executionTime = time.Now().Add(interval)
	job.id = atomic.AddUint64(&t.jobid, 1)
	t.jobs.Store(job.id, job)
	t.queue.AddAfter(job.id, interval)
	log.Tracef("添加定时器 %d interval %d", job.id, interval)
	return job, true
}

//delete job
func (t *Table) DeleteJob(job *Job) {
	if _, ok := t.jobs.Load(job.id); ok {
		//log
		log.Tracef("delete table %d job %d", t.id, job.id)
		//delete key
		t.queue.Done(job.id)
		//delete
		t.jobs.Delete(job.id)
		//put
		t.jobPool.Put(job)
	}
}

//run worker
func (t *Table) runWorker() {
	for t.processNextItem() {
	}
}

//process next item
func (t *Table) processNextItem() bool {
	// get key
	key, quit := t.queue.Get()
	if quit {
		close(t.stopCh)
		log.Warnf("table %d queue is quit", t.id)
		return false
	}

	//delete key
	t.queue.Done(key)

	//load key
	j, ok := t.jobs.Load(key)
	if !ok {
		return false
	}

	//delete
	t.jobs.Delete(key)

	//job
	job, ok := j.(*Job)
	if !ok {
		return false
	}

	//job log
	log.Tracef("=========run table %d timer job %d kind %d============", t.id, job.id, job.kind)

	//check
	switch job.kind {
	case Match:
		t.onMatch(job)
		break
	case Online:
		t.onOnline(job)
		break
	case Offline:
		t.onOffline(job)
		break
	case Message:
		t.onMessage(job)
		break
	case Timer:
		t.onTimer(job)
		break
	case Close:
		t.onClose(job)
		break
	}

	//log
	log.Tracef("=========delete table %d job %d=========", t.id, job.id)

	//put
	t.jobPool.Put(job)
	return true
}

//on message
func (t *Table) onMessage(job *Job) {
	//set player
	player, ok := t.players[job.playerID]
	if !ok {
		return
	}

	msg := new(appMessage.FrameMsg)
	if err := proto.Unmarshal(job.body, msg); err != nil {
		log.Errorf("unmarshal errr %s", err.Error())
		return
	}

	//heart mesaage
	if msg.MainCmd == int32(appMessage.MSGTYPE_VOID) {
		player.SendPlayerMsg(int32(appMessage.MSGTYPE_VOID), 2, nil)
		return
	}

	//frame message
	if msg.MainCmd == int32(appMessage.MSGTYPE_MAINCMD) {
		switch msg.SubCmd {
		case int32(appMessage.MSGKIND_READY):
			//ready
			if !t.handler.UserReady(player) {
				log.Warnf("platform[%d] room[%d] table[%d] player[%d]，logic reday fail", t.config.PlatformID, t.config.RoomID, t.id, player.PlayerID)
				return
			}

			//send ready message
			player.SendMainMsg(int32(appMessage.MSGKIND_READY), &appMessage.S2CReady{UserId: int64(player.PlayerID)})

			//start game
			t.handler.GameStart()
			log.Tracef("platform[%d] room[%d] table[%d] player[%d] send reday message", t.config.PlatformID, t.config.RoomID, t.id, player.PlayerID)
		case int32(appMessage.MSGKIND_LEAVE):
			//player leave
			if t.handler.UserLeaveGame(player) == false {
				leave := &appMessage.S2CGeneralError{EType: 8, Descript: "no exit in the game"}
				player.SendMainMsg(int32(appMessage.MSGKIND_ERROR), leave)
				return
			}

			//log
			log.Tracef("player %d leave game is robot %v", player.PlayerID, player.IsRobot())

			//player leave
			t.playerLeave(player)
		}
		return
	}

	//loginc message
	if msg.MainCmd == int32(appMessage.MSGTYPE_SUBCMD) {
		//logic message
		t.handler.OnGameMessage(msg.SubCmd, msg.Buff, player)
		return
	}
}

//on match
func (t *Table) onMatch(job *Job) {
	//match fail
	defer func() {
		if t.PlayerCount() <= 0 && t.config.IsAllowClose {
			t.Close()
		}
	}()

	//check queue is close
	if t.queue.ShuttingDown() || t.isclose == true {
		job.result <- &result{isStop: false, err: errors.New("the table is close")}

		log.Warnf("platform[%d] room[%d] table[%d] player[%d] is close", t.config.PlatformID, t.config.RoomID, t.id, job.player.PlayerID)
		return
	}

	//check people num
	if t.config.MaxPeople <= int32(len(t.players)+len(t.matchplayers)) {

		job.result <- &result{isStop: false, err: errors.New("players have been maximized")}

		log.Warnf("platform[%d] room[%d] table[%d] maxpeople[%d]>count[%d]，player[%d] cant sit", t.config.PlatformID, t.config.RoomID, t.id, len(t.players)+len(t.matchplayers), t.config.MaxPeople, job.player.PlayerID)
		return
	}

	//check chair
	if len(t.chairs) <= 0 {
		job.result <- &result{isStop: false, err: errors.New("no chair assignment")}

		log.Warnf("platform[%d] room[%d] table[%d] player[%d] no chair assignment", t.config.PlatformID, t.config.RoomID, t.id, job.player.PlayerID)
		return
	}

	//set chair
	for vli := range t.chairs {
		job.player.SetChairID(vli)
		break
	}

	//user sit down
	switch t.handler.OnActionUserSitDown(job.player, job.player.GetChairID(), t.config.AdviceConfig) {
	case SitDownOk:
		log.Tracef("platform[%d] room[%d] table[%d] match player[%v] success", t.config.PlatformID, t.config.RoomID, t.id, job.player)

		//delete chair
		delete(t.chairs, job.player.GetChairID())

		//set player
		t.matchplayers[job.player.PlayerID] = job.player

		//on timer
		t.OnTimer(time.Minute, 1, func(param interface{}) {
			t.matchPlayerLeave(param.(uint32))
		}, job.player.PlayerID)

		//return
		job.result <- &result{isStop: true, err: nil}
		return
	case SitDownErrorNomal:
		log.Warnf("platform[%d] room[%d] table[%d] match player[%d] fail", t.config.PlatformID, t.config.RoomID, t.id, job.player.PlayerID)

		//return
		job.result <- &result{isStop: false, err: errors.New("logic does not allow you to sit down ")}
		return
	case SitDownErrorOver:
		log.Warnf("platform[%d] room[%d] table[%d] match player[%d] fail", t.config.PlatformID, t.config.RoomID, t.id, job.player.PlayerID)

		//return
		job.result <- &result{isStop: true, err: errors.New("logic does not allow you to sit down ")}
		return
	default:
		panic("func OnActionUserSitDown return value error")
	}
}

//online
func (t *Table) onOnline(job *Job) {
	//get player
	player, ok := t.matchplayers[job.playerID]
	if !ok {
		//get player
		player, ok = t.players[job.playerID]
		if !ok {
			if job.conn != nil {
				job.conn.Close()
			}
			return
		}
	}

	//set conn
	player.SetOnline(job.conn)

	//delete
	delete(t.matchplayers, player.PlayerID)

	//set player
	t.players[player.PlayerID] = player

	//send scene
	t.handler.SendScene(player)

	//send player enter
	if err := player.SendMainMsg(
		int32(appMessage.MSGKIND_MATCH),
		&appMessage.S2CEnter{
			UserId:   int64(player.PlayerID), //playerID
			Head:     player.Avatar,          //头像url
			Gold:     player.Balance,         //player金币
			NickName: player.Nick,            //player昵称
			Sign:     player.Sign,            //player签名
			TableId:  int32(t.id),            //table号
			SetId:    int32(1),               //椅子号
		},
	); err != nil {
		log.Errorf("player %d send main msg is error %s", player.PlayerID, err.Error())
		return
	}

	//log
	log.Tracef("player %d is online", player.PlayerID)
}

//online
func (t *Table) onOffline(job *Job) {
	//set player
	player, ok := t.players[job.playerID]
	if !ok {
		if _, err := t.platform.PlayerLeaveGame(context.Background(), job.playerID, false); err != nil {
			log.Warnf("player %d leave game error %s", job.playerID, err.Error())
		}
		return
	}

	//check conn
	if player.CheckConn(job.conn) == false {
		return
	}

	//set offline
	player.SetOffline()

	//user exit
	if t.handler.UserOffline(player) == false {
		log.Warnf("logic does not agree with the player %d to leave", player.PlayerID)
		return
	}

	//log
	log.Tracef("player %d leave game is robot %v", player.PlayerID, player.IsRobot())

	//leave player
	t.playerLeave(player)
}

//on timer
func (t *Table) onTimer(job *Job) {
	job.f(job.param)

	if job.count == 0 {
		t.OnTimer(job.interval, job.count, job.f, job.param)
		return
	}
	if job.count-1 > 0 {
		log.Tracef("table %d 添加循环定时器剩余次数 %d", job.count-1)
		t.OnTimer(job.interval, job.count-1, job.f, job.param)
		return
	}
}

//on close
func (t *Table) onClose(job *Job) {
	//set close
	t.isclose = true

	t.config.IsOpenAiRobot = false

	t.handler.CloseTable()
}

//player leave
func (t *Table) playerLeave(player *playerObject.Player) {

	//upload player settle info
	if player.IsRobot() == false {
		if _, err := t.platform.UploadPlayerSettleInfo(context.Background(), player.PlayerID, player.ChangeBalance, player.Chip); err != nil {
			log.Warnf("player %d leave game error %s", player.PlayerID, err.Error())
		}
	}

	//delete player match
	if _, err := t.platform.PlayerLeaveGame(context.Background(), player.PlayerID, player.IsRobot()); err != nil {
		log.Warnf("player %d leave game error %s", player.PlayerID, err.Error())
	}

	//notify players to leave
	leave := &appMessage.S2CLeave{UserId: int64(player.PlayerID)}
	for _, player := range t.players {
		if err := player.SendMainMsg(int32(appMessage.MSGKIND_LEAVE), leave); err != nil {
			log.Warnf("player %d send main msg error %s", player.PlayerID, err.Error())
		}
	}

	//set offline
	player.SetOffline()

	//return chair
	t.chairs[player.GetChairID()] = player.GetChairID()

	//delete
	delete(t.players, player.PlayerID)

	//delete matchplayers
	delete(t.matchplayers, player.PlayerID)

	//log
	log.Tracef("player %d leave game", player.PlayerID)
}

//match player leave
func (t *Table) matchPlayerLeave(playerID uint32) {
	if player, ok := t.matchplayers[playerID]; ok {
		//log
		log.Warnf("player %d is match leave table id %d", playerID, t.id)

		//delete player match
		if _, err := t.platform.PlayerLeaveGame(context.Background(), player.PlayerID, player.IsRobot()); err != nil {
			log.Warnf("player %d leave game error %s", player.PlayerID, err.Error())
		}

		//return chair
		t.chairs[player.GetChairID()] = player.GetChairID()

		//set offline
		player.SetOffline()

		//delete player match
		delete(t.matchplayers, playerID)

		//close
		if t.PlayerCount() <= 0 && t.config.IsAllowClose {
			t.Close()
		}
	}
}

//check robot number
func (t *Table) checkRobot() {
	if t.config.IsOpenAiRobot == false {
		for _, player := range t.players {
			if player.IsRobot() {
				//user exit
				if t.handler.UserLeaveGame(player) == false {
					log.Warnf("logic does not agree with the robot %d to leave", player.PlayerID)
					continue
				}
				//leave player
				t.playerLeave(player)
			}
		}
		return
	}

	//auto get robot
	if len(t.config.Robot) > time.Now().Hour() {
		online := uint32(len(t.players) + len(t.matchplayers))
		robot := t.config.Robot[time.Now().Hour()]
		targetonline := robot.Max
		if robot.Max > robot.Min {
			targetonline = uint32(rand.Intn(int(robot.Max-robot.Min))) + robot.Min
		}

		//log
		log.Tracef("adjust the number of robots online %d targetonline %d", online, targetonline)

		//add
		if online > targetonline {
			//- robot
			reduce := int(online - targetonline)

			if reduce > 0 {
				for _, player := range t.players {
					if player.IsRobot() {
						//user exit
						if t.handler.UserLeaveGame(player) == false {
							log.Warnf("logic does not agree with the robot %d to leave", player.PlayerID)
							continue
						}
						//leave player
						t.playerLeave(player)

						//-
						reduce--

						//number
						if reduce <= 0 {
							break
						}
					}
				}
			}
		}

		if online < targetonline {
			//+ robot
			if err := t.GetRobot(targetonline-online, t.config.RobotMinBalance, t.config.RobotMaxBalance); err != nil {
				log.Warnf("get robot error %s", err.Error())
				return
			}

		}
	}
}

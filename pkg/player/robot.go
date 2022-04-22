package player

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"github.com/kubegames/kubegames-sdk/app/message"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
)

const (
	Message Kind = 1
	Timer   Kind = 2
)

type (
	//RobotHandler 游戏AI逻辑需要实现到接口
	RobotHandler interface {
		/*
		*游戏逻辑消息
		*参数说明:
		*@param:subCmd			消息类型
		*@param:buffer			消息体
		*@param:user			用户
		 */
		OnGameMessage(subCmd int32, buffer []byte)
	}

	//kind
	Kind int

	//match kind
	MatchKind int

	//job
	Job struct {
		//id
		id uint64
		//kind
		kind Kind
		//maincmd
		mainCmd int32
		//subcmd
		subCmd int32
		//body
		body []byte
		//count
		count uint64
		//param
		param interface{}
		//interval
		interval time.Duration
		//timer func
		f func(param interface{})
	}

	RobotInterface interface {
		//GetIP 获取ip
		GetIP() string

		//GetID 获取用户ID
		GetID() int64

		//GetScore 获取用户积分
		GetScore() int64

		//GetRoomID 获取房间ID
		GetRoomID() uint32

		//SendMsgToServer 发送消息到服务器
		SendMsgToServer(subCmd int32, pb proto.Message) error

		//AddTimer 添加一次性定时器
		//参数说明:
		//@param:interval	time.Duration时间
		//@param:jobFunc		回调业务函数
		//返回值说明:
		//@return:job   		定时器任务对象
		//@return:ok   		是否成功
		AddTimer(interval int64, jobFunc func()) (job *Job, ok bool)

		//AddTimerParam 添加带参数的一次性定时器
		//参数说明:
		//@param:interval	time.Duration时间
		//@param:jobFunc		回调业务函数
		//@param:param 		回调业务函数参数
		//返回值说明:
		//@return:job   		定时器任务对象
		//@return:ok   		是否成功
		AddTimerParam(actionInterval int64, jobFunc func(interface{}), param interface{}) (job *Job, ok bool)

		//AddTimerRepeat 添加多次定时器
		//参数说明:
		//@param:interval	time.Duration时间
		//@param:num			执行次数（为0表示无限循环）
		//@param:jobFunc		回调业务函数
		//返回值说明:
		//@return:job   		定时器任务对象
		//@return:ok   		是否成功
		AddTimerRepeat(interval int64, num uint64, jobFunc func()) (job *Job, inserted bool)

		//AddTicker 添加一个循环定时器
		//参数说明:
		//@param:interval	time.Duration时间
		//@param:jobFunc		回调业务函数
		//返回值说明:
		//@return:job   		定时器任务对象
		//@return:ok   		是否成功
		AddTicker(interval int64, jobFunc func()) (job *Job, inserted bool)

		//delete job
		DeleteJob(job *Job)

		//LeaveRoom 机器人离开房间
		LeaveRoom() error
	}

	Server interface {
		//on message
		OnMessage(playerID uint32, buff []byte) error

		//on offline
		OnOffline(playerID uint32, conn *websocket.Conn) error
	}

	//机器人
	Robot struct {
		//server
		server Server
		//player 玩家
		player *Player
		//table逻辑处理
		handler RobotHandler
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
	}
)

//new robot
func NewRobot(player *Player, server Server) *Robot {
	return &Robot{
		player: player,
		server: server,
		jobid:  0,
		stopCh: make(chan struct{}),
		jobPool: sync.Pool{
			New: func() interface{} {
				return &Job{}
			},
		},
	}
}

//bind handler
func (r *Robot) BindHandler(handler RobotHandler) {
	r.handler = handler
}

//close
func (r *Robot) OnClose() {
	if err := r.server.OnOffline(uint32(r.player.GetID()), nil); err != nil {
		log.Warnf("robot %d on offline error %s", r.player.GetID(), err.Error())
	}
	r.queue.ShutDown()
}

//on message
func (r *Robot) OnMessage(mainCmd int32, subCmd int32, buff []byte) error {
	//check shutting down
	if r.queue.ShuttingDown() {
		return errors.New("queue is shutting")
	}

	//get job
	job, ok := r.jobPool.Get().(*Job)
	if !ok {
		return errors.New("get job fail")
	}

	job.kind = Message
	job.mainCmd = mainCmd
	job.subCmd = subCmd
	job.body = buff
	job.id = atomic.AddUint64(&r.jobid, 1)
	r.jobs.Store(job.id, job)
	r.queue.Add(job.id)
	return nil
}

//on timer
func (r *Robot) OnTimer(interval time.Duration, count uint64, f func(param interface{}), param interface{}) (*Job, bool) {
	if r.queue.ShuttingDown() {
		return nil, false
	}

	job, ok := r.jobPool.Get().(*Job)
	if !ok {
		return nil, false
	}
	job.count = count
	job.kind = Timer
	job.f = f
	job.interval = interval
	job.param = param
	job.id = atomic.AddUint64(&r.jobid, 1)
	r.jobs.Store(job.id, job)
	r.queue.AddAfter(job.id, interval)
	return job, true
}

//GetIP 获取ip
func (r *Robot) GetIP() string {
	return r.player.GetIP()
}

//GetID 获取用户ID
func (r *Robot) GetID() int64 {
	return r.player.GetID()
}

//GetScore 获取用户积分
func (r *Robot) GetScore() int64 {
	return r.player.GetScore()
}

//GetRoomID 获取房间ID
func (r *Robot) GetRoomID() uint32 {
	return r.player.RoomID
}

//SendMsgToServer 发送消息到服务器
func (r *Robot) SendMsgToServer(subCmd int32, pb proto.Message) (err error) {
	if r.player.IsOnline() == false {
		//close
		r.OnClose()

		return fmt.Errorf("robot %d is offline", r.player.GetID())
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

	message, err := proto.Marshal(&message.FrameMsg{MainCmd: int32(message.MSGTYPE_SUBCMD), SubCmd: subCmd, Buff: buff, Time: time.Now().Unix()})
	if err != nil {
		log.Warnln(err.Error())
		return err
	}

	if err := r.server.OnMessage(r.player.PlayerID, message); err != nil {
		//close
		r.OnClose()

		//log
		log.Errorf(err.Error())
		return err
	}
	return nil
}

//SendMainMsgToServer 发送消息到服务器
func (r *Robot) SendMainMsgToServer(subCmd int32, pb proto.Message) (err error) {
	if r.player.IsOnline() == false {
		//close
		r.OnClose()

		return fmt.Errorf("robot %d is offline", r.player.GetID())
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

	message, err := proto.Marshal(&message.FrameMsg{MainCmd: int32(message.MSGTYPE_MAINCMD), SubCmd: subCmd, Buff: buff, Time: time.Now().Unix()})
	if err != nil {
		log.Warnln(err.Error())
		return err
	}

	if err := r.server.OnMessage(r.player.PlayerID, message); err != nil {
		//close
		r.OnClose()

		//log
		log.Errorf(err.Error())
		return err
	}
	return nil
}

//LeaveRoom 机器人离开房间
func (r *Robot) LeaveRoom() error {
	if err := r.SendMainMsgToServer(int32(message.MSGKIND_LEAVE), nil); err != nil {
		log.Errorf("SendMainMsgToServer error %s", err.Error())
		return err
	}
	return nil
}

//delete job
func (r *Robot) DeleteJob(job *Job) {
	//delete key
	r.queue.Done(job.id)
	//delete
	r.jobs.Delete(job.id)
	//put
	r.jobPool.Put(job)
}

//start
func (r *Robot) Start() {
	//wait
	wait.Until(r.runWorker, time.Millisecond, r.stopCh)

	//delete
	r.jobs.Range(func(key, value interface{}) bool {
		r.DeleteJob(value.(*Job))
		return true
	})
}

//run worker
func (r *Robot) runWorker() {
	for r.processNextItem() {
	}
}

//process next item
func (r *Robot) processNextItem() bool {
	// get key
	key, quit := r.queue.Get()
	if quit {
		close(r.stopCh)
		return false
	}

	//load key
	j, ok := r.jobs.Load(key)
	if !ok {
		return false
	}

	//job
	job := j.(*Job)

	//delete job
	defer r.DeleteJob(job)

	switch job.kind {
	case Message:
		r.onMessage(job)
	case Timer:
		r.onTimer(job)
	}
	return true
}

//OnMessage 处理消息
func (r *Robot) onMessage(job *Job) {
	switch job.mainCmd {
	case int32(message.MSGTYPE_MAINCMD):
		if job.subCmd == int32(message.MSGKIND_MATCH) {
			s2cEnter := new(message.S2CEnter)
			if err := proto.Unmarshal(job.body, s2cEnter); err != nil {
				log.Errorf(err.Error())
				return
			}
			if s2cEnter.UserId == r.GetID() {
				r.SendMainMsgToServer(int32(message.MSGKIND_READY), nil)
			}
			return
		}
	case int32(message.MSGTYPE_SUBCMD):
		if r.handler != nil {
			r.handler.OnGameMessage(job.subCmd, job.body)
		}
	}
}

//on timer
func (r *Robot) onTimer(job *Job) {
	job.f(job.param)
	if job.count > 1 {
		r.OnTimer(job.interval, job.count-1, job.f, job.param)
	}
	if job.count == 0 {
		r.OnTimer(job.interval, job.count, job.f, job.param)
	}
}

//AddTimer 添加一次性定时器
//参数说明:
//@param:interval	time.Duration时间
//@param:jobFunc		回调业务函数
//返回值说明:
//@return:job   		定时器任务对象
//@return:ok   		是否成功
func (r *Robot) AddTimer(interval int64, jobFunc func()) (job *Job, ok bool) {
	return r.OnTimer(time.Millisecond*time.Duration(interval), 1, func(param interface{}) {
		jobFunc()
	}, nil)
}

//AddTimerParam 添加带参数的一次性定时器
//参数说明:
//@param:interval	time.Duration时间
//@param:jobFunc		回调业务函数
//@param:param 		回调业务函数参数
//返回值说明:
//@return:job   		定时器任务对象
//@return:ok   		是否成功
func (r *Robot) AddTimerParam(actionInterval int64, jobFunc func(interface{}), param interface{}) (job *Job, ok bool) {
	return r.OnTimer(time.Millisecond*time.Duration(actionInterval), 1, jobFunc, param)
}

//AddTimerRepeat 添加多次定时器
//参数说明:
//@param:interval	time.Duration时间
//@param:num			执行次数（为0表示无限循环）
//@param:jobFunc		回调业务函数
//返回值说明:
//@return:job   		定时器任务对象
//@return:ok   		是否成功
func (r *Robot) AddTimerRepeat(interval int64, num uint64, jobFunc func()) (job *Job, inserted bool) {
	return r.OnTimer(time.Millisecond*time.Duration(interval), num, func(param interface{}) {
		jobFunc()
	}, nil)
}

//AddTicker 添加一个循环定时器
//参数说明:
//@param:interval	time.Duration时间
//@param:jobFunc		回调业务函数
//返回值说明:
//@return:job   		定时器任务对象
//@return:ok   		是否成功
func (r *Robot) AddTicker(interval int64, jobFunc func()) (job *Job, inserted bool) {
	return r.OnTimer(time.Millisecond*time.Duration(interval), 0, func(param interface{}) {
		jobFunc()
	}, nil)
}

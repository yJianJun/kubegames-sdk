package platform

import (
	"context"
	"time"

	"github.com/kubegames/kubegames-hall/app/model/player"
	"github.com/kubegames/kubegames-hall/app/model/room"
	"github.com/kubegames/kubegames-hall/app/service/platform"
	"github.com/kubegames/kubegames-hall/app/service/platform/types"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	"google.golang.org/grpc"
)

type Platform interface {
	//申请机器人
	ApplyRobot(ctx context.Context, in *types.ApplyRobotRequest) (*types.ApplyRobotResponse, error)
	//玩家离开游戏
	PlayerLeaveGame(ctx context.Context, playerid uint32, isRobot bool) (bool, error)
	// 广播
	Broadcast(ctx context.Context, buff []byte) (bool, error)
	//获取房间列表
	FindRooms(ctx context.Context, gameid uint32) ([]*room.Room, error)
	//运行房间
	RunningRoom(ctx context.Context, roomid uint32, ip string) (bool, error)
	//抢占房间
	AcquireRoom(ctx context.Context, roomid uint32, ip string) (bool, error)
	//释放房间
	ReleaseRoom(ctx context.Context, roomid uint32, ip string) (bool, error)
	//战绩上传
	UploadPlayerRecord(ctx context.Context, gameid, roomid uint32, records []*PlayerRecord) (bool, error)
	//用户结算信息上传
	UploadPlayerSettleInfo(ctx context.Context, playerID uint32, balance int64, chip int64) (bool, error)
	//获取血池
	GetRoomPool(ctx context.Context, roomID uint32) (int64, error)
}

type platformImpl struct {
	//hall client
	platform platform.PlatformServiceClient
}

func NewPlatform(adderss string) Platform {
	//connect hall
	conn, err := grpc.Dial(adderss, grpc.WithInsecure())
	if err != nil {
		panic(err.Error())
	}

	return &platformImpl{
		platform: platform.NewPlatformServiceClient(conn),
	}
}

//申请机器人
func (impl *platformImpl) ApplyRobot(ctx context.Context, in *types.ApplyRobotRequest) (*types.ApplyRobotResponse, error) {
	rsp, err := impl.platform.ApplyRobot(ctx, in)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

//玩家离开游戏
func (impl *platformImpl) PlayerLeaveGame(ctx context.Context, playerid uint32, isRobot bool) (bool, error) {
	rsp, err := impl.platform.PlayerLeaveGame(ctx, &types.PlayerLeaveGameRequest{PlayerID: playerid, IsRobot: isRobot})
	if err != nil {
		return false, err
	}
	return rsp.Success, nil
}

// 广播
func (impl *platformImpl) Broadcast(ctx context.Context, buff []byte) (bool, error) {
	rsp, err := impl.platform.Broadcast(ctx, &types.BroadcastRequest{Buff: buff})
	if err != nil {
		return false, err
	}
	return rsp.Success, nil
}

//获取房间列表
func (impl *platformImpl) FindRooms(ctx context.Context, gameid uint32) ([]*room.Room, error) {
	rsp, err := impl.platform.FindRooms(ctx, &types.FindRoomsRequest{GameID: gameid})
	if err != nil {
		return nil, err
	}
	return rsp.Rooms, nil
}

//运行房间
func (impl *platformImpl) RunningRoom(ctx context.Context, roomid uint32, ip string) (bool, error) {
	rsp, err := impl.platform.RunningRoom(ctx, &types.RunningRoomRequest{RoomID: roomid, Ip: ip})
	if err != nil {
		return false, err
	}
	return rsp.Success, nil
}

//抢占房间
func (impl *platformImpl) AcquireRoom(ctx context.Context, roomid uint32, ip string) (bool, error) {
	rsp, err := impl.platform.AcquireRoom(ctx, &types.AcquireRoomRequest{RoomID: roomid, Ip: ip})
	if err != nil {
		return false, err
	}
	return rsp.Success, nil
}

//释放房间
func (impl *platformImpl) ReleaseRoom(ctx context.Context, roomid uint32, ip string) (bool, error) {
	rsp, err := impl.platform.ReleaseRoom(ctx, &types.ReleaseRoomRequest{RoomID: roomid, Ip: ip})
	if err != nil {
		return false, err
	}
	return rsp.Success, nil
}

//玩家战绩
type PlayerRecord struct {
	//玩家 id
	PlayerID uint32
	//局号
	GameNum string
	//盈利
	ProfitAmount int64
	//总下注
	BetsAmount int64
	//总抽水
	DrawAmount int64
	//总产出
	OutputAmount int64
	//结算牌
	//EndCards string
	//用户当前金币
	Balance int64
	//更新时间
	UpdatedAt time.Time
	//创建时间
	CreatedAt time.Time
}

//战绩上传
func (impl *platformImpl) UploadPlayerRecord(ctx context.Context, gameid, roomid uint32, records []*PlayerRecord) (bool, error) {
	request := new(types.UploadPlayerRecordRequest)
	for _, record := range records {
		request.Records = append(request.Records, &player.PlayerRecord{
			PlayerID:     record.PlayerID,
			GameNum:      record.GameNum,
			ProfitAmount: record.ProfitAmount,
			BetsAmount:   record.BetsAmount,
			DrawAmount:   record.DrawAmount,
			OutputAmount: record.OutputAmount,
			Balance:      record.Balance,
			GameID:       gameid,
			RoomID:       roomid,
			UpdatedAt:    record.UpdatedAt,
			CreatedAt:    record.CreatedAt,
		})
	}
	rsp, err := impl.platform.UploadPlayerRecord(ctx, request)
	if err != nil {
		return false, err
	}
	return rsp.Success, nil
}

//用户结算信息上传
func (impl *platformImpl) UploadPlayerSettleInfo(ctx context.Context, playerID uint32, balance int64, chip int64) (bool, error) {
	rsp, err := impl.platform.UploadPlayerSettleInfo(ctx, &types.UploadPlayerSettleInfoRequest{
		PlayerID: playerID,
		Balance:  balance,
		Chip:     chip,
	})
	if err != nil {
		return false, err
	}
	log.Tracef("upload player settle info player %d balance %d chip %d", playerID, balance, chip)
	return rsp.Success, nil
}

//获取血池
func (impl *platformImpl) GetRoomPool(ctx context.Context, roomID uint32) (int64, error) {
	rsp, err := impl.platform.GetRoomPool(ctx, &types.GetRoomPoolRequest{
		RoomID: roomID,
	})
	if err != nil {
		return 0, err
	}
	return rsp.Value, nil
}

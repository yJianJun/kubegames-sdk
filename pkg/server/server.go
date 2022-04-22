package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	matchService "github.com/kubegames/kubegames-hall/app/service/match"
	matchTypes "github.com/kubegames/kubegames-hall/app/service/match/types"
	gameService "github.com/kubegames/kubegames-operator/app/game"
	gameTypes "github.com/kubegames/kubegames-operator/app/game/types"
	"github.com/kubegames/kubegames-sdk/internal/pkg/des"
	"github.com/kubegames/kubegames-sdk/internal/pkg/server"
	"github.com/kubegames/kubegames-sdk/pkg/config"
	"github.com/kubegames/kubegames-sdk/pkg/log"
	"github.com/kubegames/kubegames-sdk/pkg/player"
	playerObject "github.com/kubegames/kubegames-sdk/pkg/player"
)

var (
	jwtkey        = []byte("games.kubegames.com")
	Authorization = "Authorization"
	desEngine     = des.Engine([]byte("gamehall"), []byte("gamehall"))
	upGrader      = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

//claims
type Claims struct {
	PlayerID uint32
	TableID  int64
	jwt.StandardClaims
}

//jtw marshal
func JwtMarshal(playerID uint32, tableID int64) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &Claims{
		PlayerID: playerID,
		TableID:  tableID,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(7 * 24 * time.Hour).Unix(),
			IssuedAt:  time.Now().Unix(),
			Issuer:    "games",
			Subject:   "games token",
		},
	})

	tokenString, err := token.SignedString(jwtkey)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

//jwt unmarshal
func JwtUnMarshal(tokenString string) (uint32, int64, error) {
	claims := new(Claims)
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (i interface{}, err error) {
		return jwtkey, nil
	})
	if err != nil {
		return 0, 0, err
	}
	if !token.Valid {
		return 0, 0, errors.New("permissions error")
	}

	return claims.PlayerID, claims.TableID, nil
}

type (
	Server interface {
		//run server
		Run(ip string, port int) error
	}

	// server handler message
	ServerHandler interface {

		//InitConfig
		InitConfig(s Server, config []byte) error

		//delete game
		Delete(s Server, gameid string) error

		//match
		Match(s Server, player *playerObject.Player) (int64, bool, error)

		//online
		Online(s Server, playerID uint32, tableID int64, conn *websocket.Conn) error

		//offline
		Offline(s Server, playerID uint32, tableID int64, conn *websocket.Conn) error

		//message
		Message(s Server, playerID uint32, tableID int64, buff []byte) error
	}

	// server impl
	ServerImpl struct {
		config  config.Config
		server  server.Server
		handler ServerHandler
		name    string
		address string
	}
)

//new server
func NewServer(handler ServerHandler, name string) Server {
	return &ServerImpl{
		handler: handler,
		server:  server.NewServer(),
		config:  config.NewConfig(),
		name:    name,
	}
}

//run server
func (s *ServerImpl) Run(ip string, port int) error {
	//set address
	s.address = fmt.Sprintf("%s:%d", ip, port)

	//load config
	s.config.LoadConfig()

	//init conifg
	if err := s.handler.InitConfig(s, s.config.Config()); err != nil {
		panic(err.Error())
	}

	//register pprof
	pprof.Register(s.server.HttpServer())

	//register websocket
	s.server.HttpServer().GET("/ws", s.Websocket)

	//register server
	matchService.RegisterMatchServiceServer(s.server.GrpcServer(), s)
	gameService.RegisterGameServiceServer(s.server.GrpcServer(), s)

	if err := s.server.ListenHttpAndGrpcServe(fmt.Sprintf(":%d", port)); err != nil {
		log.Errorf("run kubegames sdk server err %s", err.Error())
		return err
	}
	return nil
}

//delete server
func (s *ServerImpl) Delete(ctx context.Context, request *gameTypes.DeleteRequest) (response *gameTypes.DeleteResponse, err error) {
	log.Tracef("delete game server %s", request.GameID)

	if err := s.handler.Delete(s, request.GameID); err != nil {
		log.Errorf("delete game server %s error %s", request.GameID, err.Error())
		return &gameTypes.DeleteResponse{Success: false}, nil
	}

	log.Tracef("delete game server %s success", request.GameID)
	return &gameTypes.DeleteResponse{Success: true}, nil
}

//Ping
func (s *ServerImpl) Ping(context.Context, *matchTypes.PingRequest) (*matchTypes.PingResponse, error) {
	return &matchTypes.PingResponse{Success: true}, nil
}

//match player
func (s *ServerImpl) Match(ctx context.Context, request *matchTypes.MatchRequest) (response *matchTypes.MatchResponse, err error) {
	//new player
	player, err := player.NewPlayer(request.GameID, request.RoomID, request.Token, request.Player, false, nil)
	if err != nil {
		log.Errorf("new player error %s", err.Error())
		return &matchTypes.MatchResponse{Success: false, Stop: true, Note: err.Error()}, nil
	}

	//start match
	id, stop, err := s.handler.Match(s, player)
	if err != nil {
		log.Errorf("match player %d error %s", request.Player.PlayerID, err.Error())
		return &matchTypes.MatchResponse{Success: false, Stop: stop, Note: err.Error()}, nil
	}

	//new player token
	token, err := JwtMarshal(player.PlayerID, id)
	if err != nil {
		log.Errorf("jwt token %s error %s", player.PlayerID, err.Error())
		return &matchTypes.MatchResponse{Success: false, Note: err.Error()}, nil
	}

	log.Tracef("match player %d success token %s", player.PlayerID, token)
	return &matchTypes.MatchResponse{
		Success: true,
		Note:    "match success",
		Stop:    stop,
		Token:   token,
		PodName: s.name,
		PodIp:   s.address,
	}, nil
}

//player websocket connect
func (s *ServerImpl) Websocket(c *gin.Context) {
	//get token string
	tokenString := c.Query("token")

	//vcalidate token formate
	if tokenString == "" {
		log.Errorf("token is nil")
		return
	}

	//jwt unmarshal
	playerID, tableID, err := JwtUnMarshal(tokenString)
	if err != nil {
		log.Errorf("permissions error %s", err.Error())
		c.JSON(http.StatusUnauthorized, gin.H{"code": http.StatusUnauthorized, "msg": "permissions error"})
		return
	}

	//websocket connect
	conn, err := upGrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Errorf("upgrade error %s", err.Error())
		return
	}
	defer conn.Close()

	log.Tracef("player %d online", playerID)

	//player online
	if err := s.handler.Online(s, playerID, tableID, conn); err != nil {
		log.Errorf("connect func player %d error %s", playerID, err.Error())
		return
	}

	for {
		//read
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Errorf("read message error %s", err.Error())
			break
		}

		//message func
		if err := s.handler.Message(s, playerID, tableID, message); err != nil {
			log.Errorf("on message err %s", err.Error())
			break
		}
	}

	//player offline
	if err := s.handler.Offline(s, playerID, tableID, conn); err != nil {
		log.Errorf("offline player %d error %s", playerID, err.Error())
	}
}

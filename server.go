package main

import (
	"fmt"
	"github.com/avast/retry-go"
	"github.com/ganlvtech/go-kahla-notify/cryptojs"
	"github.com/ganlvtech/go-kahla-notify/kahla"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
	"sync"
)

const (
	ResponseCodeOK = iota
	ResponseCodeNoAccessToken
	ResponseCodeNoContent
	ResponseCodeInvalidAccessToken
	ResponseCodeSendMessageFailed
)

type Conversation struct {
	Token          string
	ConversationID int
	UserID         string
	AesKey         string
}

type Conversations []*Conversation

type ConversationNotFound struct{}

func (*ConversationNotFound) Error() string {
	return "conversation not found"
}

func (c *Conversations) KeyByConversationID() map[int]*Conversation {
	result := make(map[int]*Conversation)
	for _, v := range *c {
		result[v.ConversationID] = v
	}
	return result
}

func (c *Conversations) GetByConversationID(conversationId int) (*Conversation, error) {
	for _, v := range *c {
		if v.ConversationID == conversationId {
			return v, nil
		}
	}
	return nil, &ConversationNotFound{}
}

func (c *Conversations) GetByToken(token string) (*Conversation, error) {
	for _, v := range *c {
		if v.Token == token {
			return v, nil
		}
	}
	return nil, &ConversationNotFound{}
}

type TokenNotExists struct{}

func (t *TokenNotExists) Error() string {
	return "token not exists"
}

type NotifyServer struct {
	email                   string
	password                string
	port                    int
	serverPath              string
	client                  *kahla.Client
	webSocket               *kahla.WebSocket
	httpServer              *http.Server
	friendRequestChan       chan struct{}
	updateConversationsChan chan struct{}
	sendNewTokensChan       chan struct{}
	conversations           *Conversations
}

func NewNotifyServer(email string, password string, port int) *NotifyServer {
	s := &NotifyServer{}
	s.email = email
	s.password = password
	s.port = port
	s.client = kahla.NewClient()
	s.webSocket = kahla.NewWebSocket()
	s.newHttpServer()
	s.friendRequestChan = make(chan struct{}, 1)
	s.updateConversationsChan = make(chan struct{}, 1)
	s.sendNewTokensChan = make(chan struct{}, 1)
	conversations := make(Conversations, 0)
	s.conversations = &conversations
	return s
}

func (s *NotifyServer) login() error {
	log.Println("Login as user:", s.email)
	err := retry.Do(func() error {
		_, err := s.client.Auth.Login(s.email, s.password)
		if err != nil {
			log.Println("Login failed:", err, "Retry.")
			return err
		}
		return nil
	})
	if err != nil {
		log.Println("Login failed too many times:", err)
		return err
	}
	log.Println("Login OK.")
	s.UpdateConversations()
	return nil
}

func (s *NotifyServer) initPusher() error {
	log.Println("Initializing pusher.")
	err := retry.Do(func() error {
		response, err := s.client.Auth.InitPusher()
		if err != nil {
			log.Println("Initialize pusher failed:", err, "Retry.")
			return err
		}
		s.serverPath = response.ServerPath
		return nil
	})
	if err != nil {
		log.Println("Initialize pusher failed too many times:", err)
		return err
	}
	log.Println("Initialize pusher OK.")
	return nil
}

// Synchronize call. Return when connection closed or disconnected.
func (s *NotifyServer) connectToPusher(interrupt <-chan struct{}) error {
	log.Println("Connecting to pusher.")
	err := retry.Do(func() error {
		go func() {
			state := <-s.webSocket.StateChanged
			if state == kahla.WebSocketStateConnected {
				log.Println("Connected to pusher OK.")
			}
		}()
		err := s.webSocket.Connect(s.serverPath, interrupt)
		if err != nil {
			if s.webSocket.State == kahla.WebSocketStateClosed {
				log.Println("Interrupt:", err)
				return nil
			} else if s.webSocket.State == kahla.WebSocketStateDisconnected {
				log.Println("Disconnected:", err, "Retry.")
				return err
			}
			log.Println("State:", s.webSocket.State, "Error:", err, "Retry.")
			return err
		}
		log.Println("Interrupt.")
		return nil
	})
	if err != nil {
		log.Println("Connected to pusher failed too many times:", err)
		return err
	}
	return nil
}

func (s *NotifyServer) runWebSocket(interrupt <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		err := s.login()
		if err != nil {
			continue
		}
		err = s.initPusher()
		if err != nil {
			continue
		}
		err = s.connectToPusher(interrupt)
		if err != nil {
			continue
		}
		// Interrupt
		break
	}
}

func (s *NotifyServer) runEventListener(interrupt <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-interrupt:
			log.Println("Event listener stopped.")
			return
		case i := <-s.webSocket.Event:
			switch v := i.(type) {
			case *kahla.NewMessageEvent:
				content, err := cryptojs.AesDecrypt(v.Content, v.AesKey)
				if err != nil {
					log.Println(err)
				} else {
					title := "New message"
					message := v.Sender.NickName + ": " + content
					log.Println(title, ":", message)
					if err != nil {
						log.Println(err)
					}
					if content == "refresh token" {
						conversation, err := s.conversations.GetByConversationID(v.ConversationID)
						if err != nil {
							log.Println(err)
						}
						conversation.Token = ""
						s.SendNewTokens()
					}
				}
			case *kahla.NewFriendRequestEvent:
				title := "Friend request"
				message := "You have got a new friend request!"
				log.Println(title, ":", message, "nick name:", v.Requester.NickName, "id:", v.Requester.ID)
				s.AcceptFriendRequest()
			case *kahla.WereDeletedEvent:
				title := "Were deleted"
				message := "You were deleted by one of your friends from his friend list."
				log.Println(title, ":", message, "nick name:", v.Trigger.NickName, "id:", v.Trigger.ID)
				s.UpdateConversations()
			case *kahla.FriendAcceptedEvent:
				title := "Friend request"
				message := "Your friend request was accepted!"
				log.Println(title, ":", message, "nick name:", v.Target.NickName, "id:", v.Target.ID)
			case *kahla.TimerUpdatedEvent:
				title := "Self-destruct timer updated!"
				message := fmt.Sprintf("Your current message life time is: %d", v.NewTimer)
				log.Println(title, ":", message, "conversation id:", v.ConversationID)
			default:
				panic("invalid event type")
			}
		}
	}
}

func (s *NotifyServer) newHttpServer() {
	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		c.Redirect(302, "https://github.com/ganlvtech/go-kahla-notify-server")
	})
	r.GET("/send", func(c *gin.Context) {
		token := c.Query("token")
		if token == "" {
			c.JSON(401, gin.H{
				"code":    ResponseCodeNoAccessToken,
				"message": "No access token provided.",
			})
			return
		}

		content := c.Query("content")
		if content == "" {
			c.JSON(400, gin.H{
				"code":    ResponseCodeNoContent,
				"message": "Content is required.",
			})
			return
		}

		err := s.SendMessageByToken(token, content)
		if err != nil {
			_, ok := err.(*TokenNotExists)
			if ok {
				c.JSON(401, gin.H{
					"code":    ResponseCodeInvalidAccessToken,
					"message": "Invalid access token.",
				})
				return
			}
			c.JSON(500, gin.H{
				"code": ResponseCodeSendMessageFailed,
				"msg":  "Send message failed. " + err.Error(),
			})
			return
		}
		c.JSON(200, gin.H{
			"code": ResponseCodeOK,
			"msg":  "OK",
		})
	})
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: r,
	}
}

func (s *NotifyServer) runHttpServer(interrupt <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	go func() {
		<-interrupt
		err := s.httpServer.Close()
		if err != nil {
			log.Println("Server close error.", err)
		}
	}()
	err := s.httpServer.ListenAndServe()
	if err != nil {
		if err == http.ErrServerClosed {
			log.Println("Server closed under request.")
		} else {
			log.Println("Server closed unexpect.", err)
		}
	}
}

func (s *NotifyServer) acceptFriendRequest() error {
	response, err := s.client.Friendship.MyRequests()
	if err != nil {
		log.Println("Get my friend request failed:", err)
		return err
	}
	var err1 error
	for _, v := range response.Items {
		if !v.Completed {
			_, err := s.client.Friendship.CompleteRequest(v.ID, true)
			if err != nil {
				log.Println("Complete friend request failed:", err)
				if err1 == nil {
					err1 = err
				}
				continue
			}
			log.Println("Complete friend request:", v.Creator.NickName)
			s.UpdateConversations()
		}
	}
	return err1
}

func (s *NotifyServer) AcceptFriendRequest() {
	select {
	case s.friendRequestChan <- struct{}{}:
		log.Println("New friend request task added.")
		go func() {
			err := s.acceptFriendRequest()
			if err != nil {
				log.Println(err)
			}
			<-s.friendRequestChan
		}()
	default:
		log.Println("Friend request task exists. Ignore.")
	}
}

func (s *NotifyServer) updateConversations() error {
	response, err := s.client.Friendship.MyFriends(false)
	if err != nil {
		log.Println("Update conversations failed.", err)
		return err
	}
	conversationsMap := s.conversations.KeyByConversationID()
	conversations := make(Conversations, 0, len(*s.conversations)+len(response.Items))
	for _, v := range response.Items {
		v1, ok := conversationsMap[v.ConversationID]
		if ok {
			conversations = append(conversations, v1)
		} else {
			conversations = append(conversations, &Conversation{
				ConversationID: v.ConversationID,
				Token:          "",
				AesKey:         v.AesKey,
				UserID:         v.UserID,
			})
			s.SendNewTokens()
		}
	}
	s.conversations = &conversations
	return nil
}

func (s *NotifyServer) UpdateConversations() {
	select {
	case s.updateConversationsChan <- struct{}{}:
		log.Println("Update conversation task added.")
		go func() {
			err := s.updateConversations()
			if err != nil {
				log.Println(err)
			}
			<-s.updateConversationsChan
		}()
	default:
		log.Println("Update conversation task exists. Ignore.")
	}
}

func (s *NotifyServer) sendNewTokens() error {
	var err1 error
	for _, v := range *s.conversations {
		if v.Token == "" {
			v.Token = randomString(32)
			err := s.SendMessage(v.ConversationID, v.Token, v.AesKey)
			if err != nil {
				log.Println("Send new token failed.", err, "UserID:", v.UserID)
				if err1 == nil {
					err1 = err
				}
				continue
			}
			log.Println("Send new token OK")
		}
	}
	return err1
}

func (s *NotifyServer) SendNewTokens() {
	select {
	case s.sendNewTokensChan <- struct{}{}:
		log.Println("Send new tokens task added.")
		go func() {
			err := s.sendNewTokens()
			if err != nil {
				log.Println(err)
			}
			<-s.sendNewTokensChan
		}()
	default:
		log.Println("Send new tokens task exists. Ignore.")
	}
}

func (s *NotifyServer) SendRawMessage(conversationId int, content string) error {
	err := retry.Do(func() error {
		_, err := s.client.Conversation.SendMessage(conversationId, content)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Println("Send message failed.")
		return err
	}
	return nil
}

func (s *NotifyServer) SendMessage(conversationId int, content string, aesKey string) error {
	content, err := cryptojs.AesEncrypt(content, aesKey)
	if err != nil {
		return err
	}
	return s.SendRawMessage(conversationId, content)
}

func (s *NotifyServer) SendMessageByToken(token string, content string) error {
	if s.conversations == nil {
		return &TokenNotExists{}
	}
	conversation, err := s.conversations.GetByToken(token)
	if err != nil {
		return &TokenNotExists{}
	}
	return s.SendMessage(conversation.ConversationID, content, conversation.AesKey)
}

func (s *NotifyServer) Run(interrupt <-chan struct{}) error {
	interrupt1 := make(chan struct{})
	interrupt2 := make(chan struct{})
	interrupt3 := make(chan struct{})
	go func() {
		<-interrupt
		close(interrupt1)
		close(interrupt2)
		close(interrupt3)
	}()
	done1 := make(chan struct{})
	done2 := make(chan struct{})
	done3 := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		s.runWebSocket(interrupt1, done1)
		wg.Done()
	}()
	go func() {
		s.runEventListener(interrupt2, done2)
		wg.Done()
	}()
	go func() {
		s.runHttpServer(interrupt3, done3)
		wg.Done()
	}()
	wg.Wait()
	log.Println("Kahla client stopped.")
	return nil
}

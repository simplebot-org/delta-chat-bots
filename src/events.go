package main

import (
	"encoding/base64"
	"os"
	"path/filepath"

	"github.com/deltachat/deltachat-rpc-client-go/deltachat"
	"github.com/deltachat/deltachat-rpc-client-go/deltachat/option"
	"github.com/deltachat/deltachat-rpc-client-go/deltachat/xdcrpc"
)

func onEvent(bot *deltachat.Bot, accId deltachat.AccountId, event deltachat.Event) {
	switch ev := event.(type) {
	case deltachat.EventWebxdcStatusUpdate:
		onStatusUpdate(bot.Rpc, accId, ev.MsgId, ev.StatusUpdateSerial)
	case deltachat.EventSecurejoinInviterProgress:
		if ev.Progress == 1000 {
			logger := cli.GetLogger(accId)
			logger.Debugf("QR scanned by contact with id=%v", ev.ContactId)
			chatId, err := bot.Rpc.CreateChatByContactId(accId, ev.ContactId)
			if err != nil {
				logger.Error(err)
				return
			}
			sendApp(bot.Rpc, accId, chatId)
		}
	}
}

// handle a webxdc status update
func onStatusUpdate(rpc *deltachat.Rpc, accId deltachat.AccountId, msgId deltachat.MsgId, serial uint) {
	logger := cli.GetLogger(accId).With("msg", msgId, "origin", "webxdc")
	rawUpdate, err := xdcrpc.GetUpdate(rpc, accId, msgId, serial)
	if err != nil {
		logger.Error(err)
		return
	}
	msg, err := rpc.GetMessage(accId, msgId)
	if err != nil {
		logger.Error(err)
		return
	}
	logger = logger.With("chat", msg.ChatId)
	if msg.FromId != deltachat.ContactSelf {
		logger.Debugf("Ignoring request from unofficial instance: %v", string(rawUpdate))
		return
	}
	version, err := rpc.GetWebxdcBlob(accId, msgId, "version.txt")
	if err != nil {
		logger.Error(err)
	} else {
		data, err := base64.StdEncoding.DecodeString(version)
		if err != nil {
			logger.Error(err)
		}
		version = string(data)
	}
	if version != xdcVersion {
		sendApp(rpc, accId, msg.ChatId)
		return
	}

	if xdcrpc.IsFromSelf(rawUpdate) {
		logger.Debugf("Response: %v", string(rawUpdate))
		return
	}

	logger.Debugf("Request: %v", string(rawUpdate))
	if response := xdcrpc.GetResponse(&API{}, rawUpdate); response != nil {
		err = xdcrpc.SendPayload(rpc, accId, msgId, response)
		if err != nil {
			logger.Error(err)
		}
	}
}

func onNewMsg(bot *deltachat.Bot, accId deltachat.AccountId, msgId deltachat.MsgId) {
	logger := cli.GetLogger(accId).With("msg", msgId)
	msg, err := bot.Rpc.GetMessage(accId, msgId)
	if err != nil {
		logger.Error(err)
		return
	}

	if !msg.IsBot && msg.FromId > deltachat.ContactLastSpecial && msg.Text != "" {
		chat, err := bot.Rpc.GetBasicChatInfo(accId, msg.ChatId)
		if err != nil {
			logger.Error(err)
			return
		}
		if chat.ChatType == deltachat.ChatSingle {
			logger.Debugf("Got new 1:1 message: %#v", msg)
			sendApp(bot.Rpc, accId, msg.ChatId)
		}
	}

	if msg.FromId > deltachat.ContactLastSpecial {
		err = bot.Rpc.DeleteMessages(accId, []deltachat.MsgId{msg.Id})
		if err != nil {
			logger.Error(err)
		}
	}
}

// send the app / UI interace
func sendApp(rpc *deltachat.Rpc, accId deltachat.AccountId, chatId deltachat.ChatId) {
	logger := cli.GetLogger(accId)
	// try to resend existing instance
	none := option.None[deltachat.MsgType]()
	msgIds, err := rpc.GetChatMedia(accId, chatId, deltachat.MsgWebxdc, none, none)
	if err != nil {
		logger.Error(err)
		return
	}
	for i := len(msgIds) - 1; i >= 0; i-- {
		msgId := msgIds[i]
		msg, err := rpc.GetMessage(accId, msgId)
		if err != nil {
			logger.Error(err)
			continue
		}
		if msg.FromId == deltachat.ContactSelf {
			version, err := rpc.GetWebxdcBlob(accId, msgId, "version.txt")
			if err != nil {
				logger.Error(err)
			} else {
				data, err := base64.StdEncoding.DecodeString(version)
				if err != nil {
					logger.Error(err)
				}
				version = string(data)
			}
			if version == xdcVersion {
				err = rpc.ResendMessages(accId, []deltachat.MsgId{msgId})
				if err != nil {
					logger.Error(err)
					break
				}
				return
			}
			break
		} else {
			err = rpc.DeleteMessages(accId, []deltachat.MsgId{msgId})
			if err != nil {
				logger.Error(err)
			}
		}
	}

	// no previous instance to send, send new instance

	dir, err := os.MkdirTemp("", "")
	if err != nil {
		logger.Error(err)
		return
	}
	defer os.RemoveAll(dir)

	xdcPath := filepath.Join(dir, "app.xdc")
	if err = os.WriteFile(xdcPath, xdcContent, 0666); err != nil {
		logger.Error(err)
		return
	}

	msgId, err := rpc.SendMsg(accId, chatId, deltachat.MsgData{File: xdcPath})
	if err != nil {
		logger.Error(err)
	}
	metadata := cfg.GetMetadata()
	if metadata.Data != nil {
		err := xdcrpc.SendUpdate(rpc, accId, msgId, xdcrpc.StatusUpdate[*xdcrpc.Response]{Payload: &xdcrpc.Response{Result: metadata}, Summary: xdcVersion}, "")
		if err != nil {
			logger.Error(err)
		}
	}
}

// Copyright 2017 fatedier, fatedier@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/fatedier/frp/pkg/config/types"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/metrics/mem"
	"github.com/fatedier/frp/pkg/msg"
	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/util/log"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/util"
	"github.com/fatedier/frp/pkg/util/version"
)

type GeneralResponse struct {
	Code int
	Msg  string
}

func (svr *Service) registerRouteHandlers(helper *httppkg.RouterRegisterHelper) {
	helper.Router.HandleFunc("/healthz", svr.healthz)
	subRouter := helper.Router.NewRoute().Subrouter()

	subRouter.Use(helper.AuthMiddleware.Middleware)

	// metrics
	if svr.cfg.EnablePrometheus {
		subRouter.Handle("/metrics", promhttp.Handler())
	}

	// apis
	subRouter.HandleFunc("/api/serverinfo", svr.apiServerInfo).Methods("GET")
	subRouter.HandleFunc("/api/proxy/{type}", svr.apiProxyByType).Methods("GET")
	subRouter.HandleFunc("/api/proxy/{type}/{name}", svr.apiProxyByTypeAndName).Methods("GET")
	subRouter.HandleFunc("/api/traffic/{name}", svr.apiProxyTraffic).Methods("GET")
	subRouter.HandleFunc("/api/proxies", svr.deleteProxies).Methods("DELETE")

	// 启用了webservice的通过frps的web段设置frps的代理的状态
	if svr.cfg.WebServer.EnableCreateClientProxy {
		log.Infof("enable create client proxy")
		// subRouter.HandleFunc("/api/create_proxy", svr.createClientProxies).Methods("POST")

		subRouter.HandleFunc("/api/get_clients", svr.getClientList).Methods("GET")
		subRouter.HandleFunc("/api/get_fprc_config", svr.getFrpcConfig).Methods("GET")
		subRouter.HandleFunc("/api/update_fprc_config", svr.putFrpcConfig).Methods("PUT")
	}

	// view
	subRouter.Handle("/favicon.ico", http.FileServer(helper.AssetsFS)).Methods("GET")
	subRouter.PathPrefix("/static/").Handler(
		netpkg.MakeHTTPGzipHandler(http.StripPrefix("/static/", http.FileServer(helper.AssetsFS))),
	).Methods("GET")

	subRouter.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/", http.StatusMovedPermanently)
	})
}

type serverInfoResp struct {
	Version               string `json:"version"`
	BindPort              int    `json:"bindPort"`
	VhostHTTPPort         int    `json:"vhostHTTPPort"`
	VhostHTTPSPort        int    `json:"vhostHTTPSPort"`
	TCPMuxHTTPConnectPort int    `json:"tcpmuxHTTPConnectPort"`
	KCPBindPort           int    `json:"kcpBindPort"`
	QUICBindPort          int    `json:"quicBindPort"`
	SubdomainHost         string `json:"subdomainHost"`
	MaxPoolCount          int64  `json:"maxPoolCount"`
	MaxPortsPerClient     int64  `json:"maxPortsPerClient"`
	HeartBeatTimeout      int64  `json:"heartbeatTimeout"`
	AllowPortsStr         string `json:"allowPortsStr,omitempty"`
	TLSForce              bool   `json:"tlsForce,omitempty"`

	TotalTrafficIn  int64            `json:"totalTrafficIn"`
	TotalTrafficOut int64            `json:"totalTrafficOut"`
	CurConns        int64            `json:"curConns"`
	ClientCounts    int64            `json:"clientCounts"`
	ProxyTypeCounts map[string]int64 `json:"proxyTypeCount"`
}

// /healthz
func (svr *Service) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(200)
}

// /api/serverinfo
func (svr *Service) apiServerInfo(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}
	defer func() {
		log.Infof("http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	log.Infof("http request: [%s]", r.URL.Path)
	serverStats := mem.StatsCollector.GetServer()
	svrResp := serverInfoResp{
		Version:               version.Full(),
		BindPort:              svr.cfg.BindPort,
		VhostHTTPPort:         svr.cfg.VhostHTTPPort,
		VhostHTTPSPort:        svr.cfg.VhostHTTPSPort,
		TCPMuxHTTPConnectPort: svr.cfg.TCPMuxHTTPConnectPort,
		KCPBindPort:           svr.cfg.KCPBindPort,
		QUICBindPort:          svr.cfg.QUICBindPort,
		SubdomainHost:         svr.cfg.SubDomainHost,
		MaxPoolCount:          svr.cfg.Transport.MaxPoolCount,
		MaxPortsPerClient:     svr.cfg.MaxPortsPerClient,
		HeartBeatTimeout:      svr.cfg.Transport.HeartbeatTimeout,
		AllowPortsStr:         types.PortsRangeSlice(svr.cfg.AllowPorts).String(),
		TLSForce:              svr.cfg.Transport.TLS.Force,

		TotalTrafficIn:  serverStats.TotalTrafficIn,
		TotalTrafficOut: serverStats.TotalTrafficOut,
		CurConns:        serverStats.CurConns,
		ClientCounts:    serverStats.ClientCounts,
		ProxyTypeCounts: serverStats.ProxyTypeCounts,
	}

	buf, _ := json.Marshal(&svrResp)
	res.Msg = string(buf)
}

type BaseOutConf struct {
	v1.ProxyBaseConfig
}

type TCPOutConf struct {
	BaseOutConf
	RemotePort int `json:"remotePort"`
}

type TCPMuxOutConf struct {
	BaseOutConf
	v1.DomainConfig
	Multiplexer     string `json:"multiplexer"`
	RouteByHTTPUser string `json:"routeByHTTPUser"`
}

type UDPOutConf struct {
	BaseOutConf
	RemotePort int `json:"remotePort"`
}

type HTTPOutConf struct {
	BaseOutConf
	v1.DomainConfig
	Locations         []string `json:"locations"`
	HostHeaderRewrite string   `json:"hostHeaderRewrite"`
}

type HTTPSOutConf struct {
	BaseOutConf
	v1.DomainConfig
}

type STCPOutConf struct {
	BaseOutConf
	Secretkey  string   `json:"secretKey,omitempty"`
	AllowUsers []string `json:"allowUsers,omitempty"`
}

type SUDPOutConf struct {
	BaseOutConf
	Secretkey  string   `json:"secretKey,omitempty"`
	AllowUsers []string `json:"allowUsers,omitempty"`
}

type XTCPOutConf struct {
	BaseOutConf
	Secretkey  string   `json:"secretKey,omitempty"`
	AllowUsers []string `json:"allowUsers,omitempty"`
}

func getConfByType(proxyType string) any {
	switch v1.ProxyType(proxyType) {
	case v1.ProxyTypeTCP:
		return &TCPOutConf{}
	case v1.ProxyTypeTCPMUX:
		return &TCPMuxOutConf{}
	case v1.ProxyTypeUDP:
		return &UDPOutConf{}
	case v1.ProxyTypeHTTP:
		return &HTTPOutConf{}
	case v1.ProxyTypeHTTPS:
		return &HTTPSOutConf{}
	case v1.ProxyTypeSTCP:
		return &STCPOutConf{}
	case v1.ProxyTypeSUDP:
		return &SUDPOutConf{}
	case v1.ProxyTypeXTCP:
		return &XTCPOutConf{}
	default:
		return nil
	}
}

// Get proxy info.
type ProxyStatsInfo struct {
	Name            string `json:"name"`
	Conf            any    `json:"conf"`
	ClientVersion   string `json:"clientVersion,omitempty"`
	TodayTrafficIn  int64  `json:"todayTrafficIn"`
	TodayTrafficOut int64  `json:"todayTrafficOut"`
	CurConns        int64  `json:"curConns"`
	LastStartTime   string `json:"lastStartTime"`
	LastCloseTime   string `json:"lastCloseTime"`
	Status          string `json:"status"`
}

type GetProxyInfoResp struct {
	Proxies []*ProxyStatsInfo `json:"proxies"`
}

// /api/proxy/:type
func (svr *Service) apiProxyByType(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}
	params := mux.Vars(r)
	proxyType := params["type"]

	defer func() {
		log.Infof("http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()
	log.Infof("http request: [%s]", r.URL.Path)

	proxyInfoResp := GetProxyInfoResp{}
	proxyInfoResp.Proxies = svr.getProxyStatsByType(proxyType)
	slices.SortFunc(proxyInfoResp.Proxies, func(a, b *ProxyStatsInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})

	buf, _ := json.Marshal(&proxyInfoResp)
	res.Msg = string(buf)
}

func (svr *Service) getProxyStatsByType(proxyType string) (proxyInfos []*ProxyStatsInfo) {
	proxyStats := mem.StatsCollector.GetProxiesByType(proxyType)
	proxyInfos = make([]*ProxyStatsInfo, 0, len(proxyStats))
	for _, ps := range proxyStats {
		proxyInfo := &ProxyStatsInfo{}
		if pxy, ok := svr.pxyManager.GetByName(ps.Name); ok {
			content, err := json.Marshal(pxy.GetConfigurer())
			if err != nil {
				log.Warnf("marshal proxy [%s] conf info error: %v", ps.Name, err)
				continue
			}
			proxyInfo.Conf = getConfByType(ps.Type)
			if err = json.Unmarshal(content, &proxyInfo.Conf); err != nil {
				log.Warnf("unmarshal proxy [%s] conf info error: %v", ps.Name, err)
				continue
			}
			proxyInfo.Status = "online"
			if pxy.GetLoginMsg() != nil {
				proxyInfo.ClientVersion = pxy.GetLoginMsg().Version
			}
		} else {
			proxyInfo.Status = "offline"
		}
		proxyInfo.Name = ps.Name
		proxyInfo.TodayTrafficIn = ps.TodayTrafficIn
		proxyInfo.TodayTrafficOut = ps.TodayTrafficOut
		proxyInfo.CurConns = ps.CurConns
		proxyInfo.LastStartTime = ps.LastStartTime
		proxyInfo.LastCloseTime = ps.LastCloseTime
		proxyInfos = append(proxyInfos, proxyInfo)
	}
	return
}

// Get proxy info by name.
type GetProxyStatsResp struct {
	Name            string `json:"name"`
	Conf            any    `json:"conf"`
	TodayTrafficIn  int64  `json:"todayTrafficIn"`
	TodayTrafficOut int64  `json:"todayTrafficOut"`
	CurConns        int64  `json:"curConns"`
	LastStartTime   string `json:"lastStartTime"`
	LastCloseTime   string `json:"lastCloseTime"`
	Status          string `json:"status"`
}

// /api/proxy/:type/:name
func (svr *Service) apiProxyByTypeAndName(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}
	params := mux.Vars(r)
	proxyType := params["type"]
	name := params["name"]

	defer func() {
		log.Infof("http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()
	log.Infof("http request: [%s]", r.URL.Path)

	var proxyStatsResp GetProxyStatsResp
	proxyStatsResp, res.Code, res.Msg = svr.getProxyStatsByTypeAndName(proxyType, name)
	if res.Code != 200 {
		return
	}

	buf, _ := json.Marshal(&proxyStatsResp)
	res.Msg = string(buf)
}

func (svr *Service) getProxyStatsByTypeAndName(proxyType string, proxyName string) (proxyInfo GetProxyStatsResp, code int, msg string) {
	proxyInfo.Name = proxyName
	ps := mem.StatsCollector.GetProxiesByTypeAndName(proxyType, proxyName)
	if ps == nil {
		code = 404
		msg = "no proxy info found"
	} else {
		if pxy, ok := svr.pxyManager.GetByName(proxyName); ok {
			content, err := json.Marshal(pxy.GetConfigurer())
			if err != nil {
				log.Warnf("marshal proxy [%s] conf info error: %v", ps.Name, err)
				code = 400
				msg = "parse conf error"
				return
			}
			proxyInfo.Conf = getConfByType(ps.Type)
			if err = json.Unmarshal(content, &proxyInfo.Conf); err != nil {
				log.Warnf("unmarshal proxy [%s] conf info error: %v", ps.Name, err)
				code = 400
				msg = "parse conf error"
				return
			}
			proxyInfo.Status = "online"
		} else {
			proxyInfo.Status = "offline"
		}
		proxyInfo.TodayTrafficIn = ps.TodayTrafficIn
		proxyInfo.TodayTrafficOut = ps.TodayTrafficOut
		proxyInfo.CurConns = ps.CurConns
		proxyInfo.LastStartTime = ps.LastStartTime
		proxyInfo.LastCloseTime = ps.LastCloseTime
		code = 200
	}

	return
}

// /api/traffic/:name
type GetProxyTrafficResp struct {
	Name       string  `json:"name"`
	TrafficIn  []int64 `json:"trafficIn"`
	TrafficOut []int64 `json:"trafficOut"`
}

func (svr *Service) apiProxyTraffic(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}
	params := mux.Vars(r)
	name := params["name"]

	defer func() {
		log.Infof("http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()
	log.Infof("http request: [%s]", r.URL.Path)

	trafficResp := GetProxyTrafficResp{}
	trafficResp.Name = name
	proxyTrafficInfo := mem.StatsCollector.GetProxyTraffic(name)

	if proxyTrafficInfo == nil {
		res.Code = 404
		res.Msg = "no proxy info found"
		return
	}
	trafficResp.TrafficIn = proxyTrafficInfo.TrafficIn
	trafficResp.TrafficOut = proxyTrafficInfo.TrafficOut

	buf, _ := json.Marshal(&trafficResp)
	res.Msg = string(buf)
}

// DELETE /api/proxies?status=offline
func (svr *Service) deleteProxies(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}

	log.Infof("http request: [%s]", r.URL.Path)
	defer func() {
		log.Infof("http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	status := r.URL.Query().Get("status")
	if status != "offline" {
		res.Code = 400
		res.Msg = "status only support offline"
		return
	}
	cleared, total := mem.StatsCollector.ClearOfflineProxies()
	log.Infof("cleared [%d] offline proxies, total [%d] proxies", cleared, total)
}

// 获取客户端的配置文件
func (svr *Service) getClientConfig(client_RunID string) (conf_text string, err error) {
	if ctl, ok := svr.ctlManager.GetByID(client_RunID); ok {
		transactionID, _ := util.RandID()
		msg_cmdreq := msg.CmdRequest{
			Action:        "get_frpc_config",
			TransactionID: transactionID,
			NewWorkConn:   msg.NewWorkConn{RunID: ctl.runID},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		respMsg, err := ctl.msgTransporter.Do(
			ctx,
			&msg_cmdreq,
			transactionID,           // laneKey
			msg.TypeNameCmdResponse, // recvMsgType
		)

		if err != nil {
			return "", fmt.Errorf("can not get client response: %v", err)
		} else {
			if cmdResp, ok := respMsg.(*msg.CmdResponse); ok {
				return cmdResp.Result, nil
			} else {
				return "", fmt.Errorf("respMsg is not cmdResp:%v", err)
			}
		}
	} else {
		return "", fmt.Errorf("client [%s] not found", client_RunID)
	}
}

// 设置客户端的配置文件
func (svr *Service) setClientConfig(client_RunID, conf_text string) error {
	if ctl, ok := svr.ctlManager.GetByID(client_RunID); ok {
		transactionID, _ := util.RandID()
		msg_cmdreq := msg.CmdRequest{
			Action:        "set_frpc_config",
			Params:        []string{conf_text},
			TransactionID: transactionID,
			NewWorkConn:   msg.NewWorkConn{RunID: ctl.runID},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		respMsg, err := ctl.msgTransporter.Do(
			ctx,
			&msg_cmdreq,
			transactionID,           // laneKey
			msg.TypeNameCmdResponse, // recvMsgType
		)

		if err != nil {
			return fmt.Errorf("can not get client response: %v", err)
		} else {
			if cmdResp, ok := respMsg.(*msg.CmdResponse); ok {
				if cmdResp.Error == "" {
					return nil
				} else {
					return fmt.Errorf("set client config error: %s", cmdResp.Error)
				}
			} else {
				return fmt.Errorf("respMsg is not cmdResp:%v", err)
			}
		}
	} else {
		return fmt.Errorf("client [%s] not found", client_RunID)
	}
}

// 重新加载客户端的配置文件
func (svr *Service) reloadClientConfig(client_RunID string) error {
	if ctl, ok := svr.ctlManager.GetByID(client_RunID); ok {
		transactionID, _ := util.RandID()
		msg_cmdreq := msg.CmdRequest{
			Action:        "reload_frpc_config",
			TransactionID: transactionID,
			NewWorkConn:   msg.NewWorkConn{RunID: ctl.runID},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		respMsg, err := ctl.msgTransporter.Do(
			ctx,
			&msg_cmdreq,
			transactionID,           // laneKey
			msg.TypeNameCmdResponse, // recvMsgType
		)

		if err != nil {
			return fmt.Errorf("can not get client response: %v", err)
		} else {
			if cmdResp, ok := respMsg.(*msg.CmdResponse); ok {
				if cmdResp.Error == "" {
					return nil
				} else {
					return fmt.Errorf("reload client config error: %s", cmdResp.Error)
				}
			} else {
				return fmt.Errorf("respMsg is not cmdResp:%v", err)
			}
		}
	} else {
		return fmt.Errorf("client [%s] not found", client_RunID)
	}
}

// // 创建frpc的新的proxy
// func (svr *Service) createClientProxies(w http.ResponseWriter, r *http.Request) {
// 	res := GeneralResponse{Code: 200}

// 	log.Infof("http request: [%s] [%s]", r.Method, r.URL.Path)
// 	defer func() {
// 		log.Infof("http response [%s]: code [%d]", r.URL.Path, res.Code)
// 		w.Header().Set("Content-Type", "application/json")
// 		w.WriteHeader(res.Code)
// 		if len(res.Msg) > 0 {
// 			_, _ = w.Write([]byte(res.Msg))
// 		}
// 	}()

// 	// 只处理POST请求
// 	if r.Method != "POST" {
// 		res.Code = 405
// 		res.Msg = "Method not allowed"
// 		return
// 	}

// 	// 读取请求体
// 	body, err := io.ReadAll(r.Body)
// 	if err != nil {
// 		res.Code = 400
// 		res.Msg = "Failed to read request body: " + err.Error()
// 		return
// 	}
// 	defer r.Body.Close()

// 	log.Infof("request body: %s", string(body))

// 	// 定义请求体结构
// 	type CreateProxyRequest struct {
// 		Type        string `json:"type"`
// 		Name        string `json:"name"`
// 		Description string `json:"description"`
// 	}

// 	var req CreateProxyRequest
// 	if err := json.Unmarshal(body, &req); err != nil {
// 		res.Code = 400
// 		res.Msg = "Failed to parse JSON: " + err.Error()
// 		return
// 	}

// 	// 验证必需字段
// 	if req.Type == "" || req.Name == "" {
// 		res.Code = 400
// 		res.Msg = "type and name are required"
// 		return
// 	}

// 	// 记录请求信息
// 	log.Infof("Creating proxy: type=[%s], name=[%s], description=[%s]",
// 		req.Type, req.Name, req.Description)

// 	// TODO: 这里实现实际的代理创建逻辑
// 	// 例如：调用 frps 的内部 API 创建代理
// 	// 或者通过 Plugin 系统处理

// 	log.Infof("----svr.ctlManager:%v", svr.ctlManager)
// 	// log.Infof("----svr.pxyManager:%v", svr.pxyManager)
// 	// // svr.pxyManager.Add(req.Type, req.Name, req.Description)
// 	// // 模拟创建成功（实际实现需要根据你的业务逻辑）
// 	// tcp_proxy := v1.TCPProxyConfig{ProxyBaseConfig: v1.ProxyBaseConfig{Name: "new_ssh_test", Type: "tcp", ProxyBackend: v1.ProxyBackend{LocalIP: "10.125.237.73", LocalPort: 22}}, RemotePort: 12134}

// 	// for _, ctl := range svr.ctlManager.GetAll() {
// 	// 	if ctl.loginMsg.User == "iei_pc" {
// 	// 		var msg msg.NewProxy
// 	// 		tcp_proxy.MarshalToMsg(&msg)
// 	// 		if !strings.HasPrefix(msg.ProxyName, fmt.Sprintf("%s.", ctl.loginMsg.User)) {
// 	// 			msg.ProxyName = fmt.Sprintf("%s.%s", ctl.loginMsg.User, msg.ProxyName)
// 	// 		}
// 	// 		err = ctl.SendMessage(&msg)
// 	// 		if err != nil {
// 	// 			log.Errorf("SendMessage error: %v", err)
// 	// 		}
// 	// 	}
// 	// }

// 	// // "062bea758db53df4"
// 	// // for _, ctl := range svr.ctlManager {
// 	// // 	ctl.AddProxy(&tcp_proxy)
// 	// // }

// 	// log.Infof("Proxy [%s] created successfully", req.Name)

// 	for _, ctl := range svr.ctlManager.GetAll() {
// 		log.Infof("ctl.runID:%s, UUID:%s, HostName:%s, OS User:%s", ctl.runID, ctl.loginMsg.UUID, ctl.loginMsg.HostName, ctl.loginMsg.OSUser)
// 		config_text, err := svr.getClientConfig(ctl.runID)
// 		if err != nil {
// 			result := map[string]interface{}{
// 				"success": false,
// 				"message": "获取client配置失败" + err.Error(),
// 				"data": map[string]interface{}{
// 					"type":        req.Type,
// 					"name":        req.Name,
// 					"description": req.Description,
// 				},
// 			}
// 			responseData, _ := json.Marshal(result)
// 			res.Msg = string(responseData)
// 			return
// 		} else {
// 			config_text += fmt.Sprintf("\n# add one %s", time.Now().Format("2006-01-02 15:04:05"))
// 			err2 := svr.setClientConfig(ctl.runID, config_text)
// 			if err2 != nil {
// 				log.Errorf("setClientConfig error: %v", err2)
// 			}
// 			err3 := svr.reloadClientConfig(ctl.runID)
// 			if err3 != nil {
// 				log.Errorf("reloadClientConfig error: %v", err3)
// 			}
// 			result := map[string]interface{}{
// 				"success": true,
// 				"message": "更新client配置成功",
// 				"data": map[string]interface{}{
// 					"type":        req.Type,
// 					"name":        req.Name,
// 					"description": config_text,
// 				},
// 			}
// 			responseData, _ := json.Marshal(result)
// 			res.Msg = string(responseData)
// 			return
// 		}
// 	}

// 	// 返回成功响应
// 	result := map[string]interface{}{
// 		"success": false,
// 		"message": "没有处理请求呢",
// 		"data": map[string]interface{}{
// 			"type":        req.Type,
// 			"name":        req.Name,
// 			"description": req.Description,
// 		},
// 	}

// 	responseData, _ := json.Marshal(result)
// 	res.Msg = string(responseData)
// }

// 获取客户端列表
func (svr *Service) getClientList(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}
	defer func() {
		log.Infof("http response [%s]: code [%d]", r.URL.Path, res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()
	log.Infof("http request: [%s]", r.URL.Path)

	var client_login_info []*msg.Login
	for _, ctl := range svr.ctlManager.GetAll() {

		client_login_info = append(client_login_info, ctl.loginMsg)
	}

	buf, _ := json.Marshal(&client_login_info)
	res.Msg = string(buf)
}

// 根据run_id获取客户端的配置文件
func (svr *Service) getFrpcConfig(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}

	log.Infof("http get request [/api/config]")
	defer func() {
		log.Infof("http get response [/api/config], code [%d]", res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	run_id := r.URL.Query().Get("run_id")
	if run_id == "" {
		res.Code = 400
		res.Msg = "run_id is empty"
		log.Warnf("%s", res.Msg)
		return
	}

	config_text, err := svr.getClientConfig(run_id)
	if err != nil {
		res.Code = 400
		res.Msg = err.Error()
		log.Warnf("load frpc config file error: %s", res.Msg)
		return
	}
	res.Msg = string(config_text)
}

type putFrpcConfig struct {
	RunID         string `json:"run_id"`
	ConfigContent string `json:"config_content"`
}

func (svr *Service) putFrpcConfig(w http.ResponseWriter, r *http.Request) {
	res := GeneralResponse{Code: 200}

	log.Infof("http put request [/api/config]")
	defer func() {
		log.Infof("http put response [/api/config], code [%d]", res.Code)
		w.WriteHeader(res.Code)
		if len(res.Msg) > 0 {
			_, _ = w.Write([]byte(res.Msg))
		}
	}()

	// 解析请求体中的 JSON
	body, err := io.ReadAll(r.Body)
	if err != nil {
		res.Code = 400
		res.Msg = fmt.Sprintf("read request body error: %v", err)
		log.Warnf("%s", res.Msg)
		return
	}
	defer r.Body.Close()

	if len(body) == 0 {
		res.Code = 400
		res.Msg = "body can't be empty"
		log.Warnf("%s", res.Msg)
		return
	}

	// 解析 JSON 格式的请求体
	var req putFrpcConfig
	if err := json.Unmarshal(body, &req); err != nil {
		res.Code = 400
		res.Msg = fmt.Sprintf("parse JSON error: %v", err)
		log.Warnf("%s", res.Msg)
		return
	}

	// 使用 setClientConfig 方法设置客户端配置
	if err := svr.setClientConfig(req.RunID, req.ConfigContent); err != nil {
		res.Code = 400
		res.Msg = fmt.Sprintf("set client config error: %v", err)
		log.Warnf("%s", res.Msg)
		return
	}

	// 重新加载客户端配置
	if err := svr.reloadClientConfig(req.RunID); err != nil {
		log.Warnf("reload client config warning: %v", err)
		// 不返回错误，只是记录警告
	}

	res.Msg = "frpc config update success"
}

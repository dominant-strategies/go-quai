package rpc

type StratumReq struct {
	Message jsonrpcMessage
	Worker 	string 	`json:"worker"`
}
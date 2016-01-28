package spore

import (
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
)

func AliyunClient() *ecs.Client {
	return ecs.NewClient(AliyunAppKey, AliyunAppSecrety)
}

func NewPagination() common.Pagination {
	pagination := common.Pagination{
		PageSize:   50,
		PageNumber: 1,
	}
	return pagination
}

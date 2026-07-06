package feishu

import (
	"testing"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkaccesstoken "github.com/larksuite/oapi-sdk-go/v3/core/accesstoken"
)


func withConfig(cfg *config.Config,fn func()){  //传入全局配置而不是client，传入无参数，无返回值的函数，代表需要在临时配置下执行的测试代码
	original := config.AppConfig
	config.AppConfig = cfg
	defer func(){ config.AppConfig = original}()

}


func TestInit(t *testing.T){
	//测试1：Appid
	t.Run("missing AppID",func(t *testing.T) {//创建一个子测试，名称为“missing appid”,第二个参数是匿名函数，里面是子测试的具体逻辑
		defer func ()  {//在子测试的匿名函数返回之前，延迟执行一段匿名函数，无论init()是否panic，这个difer都会执行起来，用它捕获并检查panic
			if r := recover();r==nil{//recover返回panic或nil
				t.Error("expected panic,but did not panic")
			}
		}()

		withConfig(&config.Config{
			Feishu_AppID: "111",
			Feishu_AppSecret: "111",
		},func ()  {
			Init()
		})

	})
}

func TestGetClient(t *testing.T){
	expectedClient := &Client{
		AppID:"cli_a9706ac566b9dcc7",
		AppSecret:"WXEbhEbYBEum6vj9b8HYChAnlQd4QtIU",
		RedirectURL:"111",
		 SDK:        lark.NewClient(cfl),
		
	}
	AppClient = expectedClient
	c,err:=getClient()

	
	if c != expectedClient{
		t.Errorf("got client %p, want %p", c, expectedClient)
		if err!=nil{
		t.Fatal("getClient returned nil")
	}

	}
	
}
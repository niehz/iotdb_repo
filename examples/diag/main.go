// diag 诊断程序：使用官方客户端直接执行各类查询，用于排查服务端问题
//
// 已定位结论（IoTDB 1.3.1 服务端）：
//
//  1. SELECT 列表中显式包含 time 列（如 SELECT time, temperature FROM root.x）
//     会导致服务端异常断开连接，客户端表现为 EOF —— 这是 IoTDB 1.3.1 服务端缺陷。
//     而 IoTDB 总是隐式返回 time 列（结果集第一列），因此查询时不要显式选择 time。
//     （iotdborm 库会自动过滤 Select 中的 time 列，无需手动处理）
//  2. show devices 在树形模型下报 301 语法错误，属正常现象（树形模型没有该语法）。
//
// 运行方式：
//
//	go run . -host 127.0.0.1 -port 6667 -user root -password root
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/apache/iotdb-client-go/client"
)

func main() {
	host := flag.String("host", "127.0.0.1", "IoTDB地址")
	port := flag.String("port", "6667", "IoTDB端口")
	user := flag.String("user", "root", "用户名")
	password := flag.String("password", "root", "密码")
	devicePath := flag.String("path", "root.factory.workshop01.device01", "设备路径")
	flag.Parse()

	conf := &client.PoolConfig{Host: *host, Port: *port, UserName: *user, Password: *password}
	pool := client.NewSessionPool(conf, 3, 60000, 60000, false)
	defer pool.Close()

	session, err := pool.GetSession()
	if err != nil {
		log.Fatal("GetSession: ", err)
	}
	defer pool.PutBack(session)
	fmt.Println("会话创建成功")

	fmt.Println("\n=== 正常查询（预期全部成功）===")
	var timeout int64 = 60000
	run(session, "show timeseries root.factory.**", &timeout, "")
	run(session, fmt.Sprintf("select * from %s", *devicePath), &timeout, "")
	run(session, fmt.Sprintf("select temperature from %s", *devicePath), &timeout, "")
	run(session, fmt.Sprintf("select temperature, pressure, humidity from %s", *devicePath), &timeout, "")
	run(session, fmt.Sprintf("select count(temperature) from %s", *devicePath), &timeout, "")

	fmt.Println("\n=== 已知服务端问题（IoTDB 1.3.1，预期失败）===")
	run(session, fmt.Sprintf("select time, temperature from %s", *devicePath), &timeout,
		"服务端缺陷：显式查询time列会断开连接（EOF），应避免，time列会自动返回")
	run(session, "show devices", &timeout,
		"树形模型不支持show devices，报301属正常现象")
}

func run(session client.Session, sql string, timeout *int64, note string) {
	fmt.Printf("==> %s\n", sql)
	ds, err := session.ExecuteQueryStatement(sql, timeout)
	if err != nil {
		fmt.Printf("    失败: %v\n", err)
		if note != "" {
			fmt.Printf("    说明: %s\n", note)
		}
		return
	}
	rows := 0
	for {
		next, err := ds.Next()
		if err != nil {
			fmt.Printf("    Next失败: %v\n", err)
			break
		}
		if !next {
			break
		}
		rows++
	}
	ds.Close()
	fmt.Printf("    成功: %d 行\n", rows)
}

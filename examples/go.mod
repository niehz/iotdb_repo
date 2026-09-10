module iotdborm-example

go 1.21

require (
	github.com/apache/iotdb-client-go v1.3.7
	github.com/niehz/iotdb_repo v0.0.0-00010101000000-000000000000
)

require github.com/apache/thrift v0.15.0 // indirect

replace github.com/niehz/iotdb_repo => ../

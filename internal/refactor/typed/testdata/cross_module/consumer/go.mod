module example.com/consumer

go 1.23

require example.com/producer v0.0.0

replace example.com/producer => ../producer

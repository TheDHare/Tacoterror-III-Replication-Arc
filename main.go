package main

import (
	"flag"
	"log"
	"net"
	"time"

	auction "tacoterror/grpc"
	"tacoterror/nodes"

	srv "tacoterror/server"

	"google.golang.org/grpc"
)

func main() {
	// Basic template for listening on a grpc server
	nodeId := flag.Int("id", 1, "node id")
	addr := flag.String("addr", ":5001", "listen addr")
	flag.Parse()

	n := nodes.NewNode(int64(*nodeId), true, 30*time.Second)

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer()

	auctionServer := srv.NewAuctionServer(n)

	auction.RegisterAuctionServiceServer(server, auctionServer)
	auction.RegisterReplicationServiceServer(server, auctionServer)

	log.Printf("Node %d listening on %s", *nodeId, *addr)

	if err := server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

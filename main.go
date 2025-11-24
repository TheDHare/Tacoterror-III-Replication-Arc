package main

import (
	"flag"
	"log"
	"net"
	"strings"
	"time"

	auction "tacoterror/grpc"
	"tacoterror/nodes"

	srv "tacoterror/server"

	"google.golang.org/grpc"
)

func main() {

	nodeId := flag.Int("id", 1, "node id")
	addr := flag.String("addr", ":5001", "listen addr")
	replicas := flag.String("replicas", "", "comma-separated list of follower addresses")
	leaderAddr := flag.String("leader", "", "explicit leader address (optional)")
	flag.Parse()

	// Create node
	n := nodes.NewNode(int64(*nodeId), 30*time.Second)
	n.ReplicationMgr = nodes.NewReplicationManager(n)

	// Parse follower list
	followerAddrs := []string{}
	if *replicas != "" {
		followerAddrs = strings.Split(*replicas, ",")
	}

	// Leader / follower setup
	if n.IsLeader {
		log.Printf("[MAIN] Node %d is LEADER", n.NodeID)
		n.ReplicationMgr.SetFollowers(followerAddrs)
	} else {
		// Follower needs leader addr
		if *leaderAddr == "" {
			log.Printf("[MAIN] WARNING: follower %d has no --leader provided", n.NodeID)
		} else {
			log.Printf("[MAIN] Node %d is FOLLOWER, leader at %s", n.NodeID, *leaderAddr)
			n.ReplicationMgr.SetLeader(*leaderAddr)
		}
	}

	// Start listening
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

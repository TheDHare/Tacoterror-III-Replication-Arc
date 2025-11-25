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
	leaderAddr := flag.String("leader", "", "address of the leader")
	canBeLeader := flag.Bool("canBeLeader", false, "whether this follower can inherit leadership on failure")
	flag.Parse()

	n := nodes.NewNode(int64(*nodeId), 30*time.Second)
	n.ReplicationMgr = nodes.NewReplicationManager(n)

	followerAddrs := []string{}
	if *replicas != "" {
		followerAddrs = strings.Split(*replicas, ",")
	}

	if *replicas != "" {
		n.IsLeader = true
		n.LeaderID = n.NodeID
		n.ReplicationMgr.SetFollowers(followerAddrs)
		n.ListenAddr = *addr

		log.Printf("[MAIN] Node %d is LEADER", n.NodeID)

	} else if *leaderAddr != "" {
		n.IsLeader = false
		n.LeaderID = 0
		n.ReplicationMgr.SetLeader(*leaderAddr)
		n.ReplicationMgr.SetCanBeLeader(*canBeLeader)
		if *canBeLeader {
			n.ReplicationMgr.StartLeaderMonitor()
		}
		log.Printf("[MAIN] Node %d is FOLLOWER (leader=%s, canBeLeader=%v)", n.NodeID, *leaderAddr, *canBeLeader)

	} else {
		log.Printf("[MAIN] Node %d has NO ROLE (start with --replicas or --leader)", n.NodeID)
	}

	if !n.IsLeader {
		go func() {
			time.Sleep(300 * time.Millisecond)
			n.SyncAllAuctionsFromLeader()
		}()
	}

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	go n.ReplicationMgr.StartLeaderChangeListener(*addr)

	auctionServer := srv.NewAuctionServer(n)

	auction.RegisterAuctionServiceServer(server, auctionServer)
	auction.RegisterReplicationServiceServer(server, auctionServer)

	log.Printf("Node %d listening on %s", *nodeId, *addr)

	if err := server.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

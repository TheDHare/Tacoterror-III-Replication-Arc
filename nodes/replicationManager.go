package nodes

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	auction "tacoterror/grpc"

	"google.golang.org/grpc"
)

// ReplicationManager manages bid replication between leader and followers,
// maintains leader addresses, and implements failure detection and leader promotion.
type ReplicationManager struct {
	node          *Node
	followerAddrs []string
	leaderAddr    string
	canBeLeader   bool
}

// NewReplicationManager returns a new replication manager bound to a node.
func NewReplicationManager(n *Node) *ReplicationManager {
	return &ReplicationManager{node: n}
}

// SetFollowers registers follower nodes for replication (used when this node is leader).
func (rm *ReplicationManager) SetFollowers(addrs []string) {
	rm.followerAddrs = addrs
}

// nodeAddress returns the gRPC listen address of this node.
func (rm *ReplicationManager) nodeAddress() string {
	return rm.node.ListenAddr
}

// SetLeader assigns a leader address for this follower.
func (rm *ReplicationManager) SetLeader(addr string) {
	rm.leaderAddr = addr
}

// LeaderAddr returns the current leader's address.
func (rm *ReplicationManager) LeaderAddr() string {
	return rm.leaderAddr
}

// ReplicateAuctionState sends a replicated auction state update from leader to all followers.
func (rm *ReplicationManager) ReplicateAuctionState(a *AuctionState) {
	if !rm.node.IsLeader {
		return
	}
	for _, addr := range rm.followerAddrs {
		faddr := addr
		go rm.sendReplicateBid(faddr, a)
	}
}

// sendReplicateBid delivers a replication message to a follower node.
func (rm *ReplicationManager) sendReplicateBid(addr string, a *AuctionState) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Printf("[RM] Could not connect to follower %s: %v", addr, err)
		return
	}
	defer conn.Close()

	client := auction.NewReplicationServiceClient(conn)
	resp, err := client.ReplicateBid(ctx, &auction.ReplicateBidRequest{
		AuctionId:  a.AuctionID,
		HighestBid: a.HighestBid,
		Winner:     a.Winner,
		Status:     a.Status,
		Sequence:   a.Sequence,
	})

	if err != nil {
		log.Printf("[RM] ReplicateBid to %s failed: %v", addr, err)
		return
	}

	if !resp.Success {
		log.Printf("[RM] Follower %s rejected replication: %s (last=%d)",
			addr, resp.Reason, resp.LastAppliedSequence)
	}
}

// SyncAuctionFromLeader pulls state for a given auction ID from the leader.
func (rm *ReplicationManager) SyncAuctionFromLeader(ctx context.Context, auctionID int64) error {
	if rm.leaderAddr == "" {
		return nil
	}

	conn, err := grpc.DialContext(ctx, rm.leaderAddr, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return err
	}
	defer conn.Close()

	client := auction.NewReplicationServiceClient(conn)
	resp, err := client.SyncState(ctx, &auction.SyncStateRequest{
		AuctionId: auctionID,
		NodeId:    rm.node.NodeID,
	})
	if err != nil {
		return err
	}

	rm.node.mu.Lock()
	defer rm.node.mu.Unlock()

	a, ok := rm.node.Auctions[resp.AuctionId]
	if !ok {
		a = &AuctionState{
			AuctionID: resp.AuctionId,
			EndTime:   time.Now().Add(rm.node.AuctionDuration),
		}
		rm.node.Auctions[resp.AuctionId] = a
	}

	a.HighestBid = resp.HighestBid
	a.Winner = resp.AuctionWinner
	a.Status = resp.Status
	a.Sequence = resp.LastSequence

	return nil
}

// SetCanBeLeader enables a follower to auto-promote if the leader fails.
func (rm *ReplicationManager) SetCanBeLeader(v bool) {
	rm.canBeLeader = v
}

// StartLeaderMonitor begins periodic leader heartbeat checks on followers.
func (rm *ReplicationManager) StartLeaderMonitor() {
	if rm.leaderAddr == "" || !rm.canBeLeader {
		return
	}

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		failures := 0

		for range ticker.C {
			if rm.node.IsLeader {
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			conn, err := grpc.DialContext(ctx, rm.leaderAddr, grpc.WithInsecure(), grpc.WithBlock())
			cancel()

			if err != nil {
				failures++
				if failures >= 3 {
					rm.promoteToLeader()
					return
				}
			} else {
				failures = 0
				conn.Close()
			}
		}
	}()
}

// promoteToLeader elevates this node to leader and notifies peers.
func (rm *ReplicationManager) promoteToLeader() {
	rm.node.mu.Lock()

	if rm.node.IsLeader {
		rm.node.mu.Unlock()
		return
	}

	// Prevent multiple followers from promoting simultaneously
	if !rm.lowestEligibleFollower() {
		rm.node.mu.Unlock()
		log.Printf("[RM] Node %d is NOT lowest eligible follower → not promoting", rm.node.NodeID)
		return
	}

	rm.node.IsLeader = true
	rm.node.LeaderID = rm.node.NodeID
	oldLeader := rm.leaderAddr
	rm.leaderAddr = ""
	rm.node.mu.Unlock()

	log.Printf("[RM] Node %d promoting itself to LEADER (old leader %s unreachable)",
		rm.node.NodeID, oldLeader)

	rm.broadcastNewLeader()
}

// StartLeaderChangeListener listens for TCP leader-change notifications on port+1000.
func (rm *ReplicationManager) StartLeaderChangeListener(grpcAddr string) {
	parts := strings.Split(grpcAddr, ":")
	port := parts[1]
	p, err := strconv.Atoi(port)
	if err != nil {
		log.Fatalf("invalid port %q: %v", port, err)
	}

	listenerPort := net.JoinHostPort("", strconv.Itoa(1000+p))

	ln, err := net.Listen("tcp", listenerPort)
	if err != nil {
		log.Printf("Leader change listener error: %v", err)
		return
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				continue
			}
			go rm.handleLeaderChangeConn(conn)
		}
	}()
}

// handleLeaderChangeConn processes a received leader-change message.
func (rm *ReplicationManager) handleLeaderChangeConn(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	msg := string(buf[:n])

	if strings.HasPrefix(msg, "NEW_LEADER:") {
		newLeader := strings.TrimPrefix(msg, "NEW_LEADER:")
		rm.node.IsLeader = false
		rm.leaderAddr = newLeader
		log.Printf("[RM] Node %d updated leader to %s", rm.node.NodeID, newLeader)
	}
}

// broadcastNewLeader notifies all peers about the newly promoted leader.
func (rm *ReplicationManager) broadcastNewLeader() {
	for _, addr := range append(rm.followerAddrs, rm.leaderAddr) {
		if addr == "" {
			continue
		}

		go func(a string) {
			host, port, _ := net.SplitHostPort(a)
			p, _ := strconv.Atoi(port)
			target := net.JoinHostPort(host, fmt.Sprintf("%d", p+1000))

			conn, err := net.Dial("tcp", target)
			if err != nil {
				return
			}
			defer conn.Close()

			msg := "NEW_LEADER:" + rm.nodeAddress()
			conn.Write([]byte(msg))
		}(addr)
	}
}

// lowestEligibleFollower checks whether this follower is the lowest-ID node
// among the followers that are allowed to become leader.
// Only this follower may promote itself if the leader dies.
// This prevents multiple concurrent promotions (split-brain).
func (rm *ReplicationManager) lowestEligibleFollower() bool {
	myID := rm.node.NodeID
	lowest := myID

	for _, addr := range rm.followerAddrs {
		id, err := rm.extractNodeID(addr)
		if err != nil {
			continue
		}

		// Only allow nodes that explicitly opted in for leader inheritance
		if id < lowest && rm.isEligible(id) {
			lowest = id
		}
	}

	return myID == lowest
}

// extractNodeID parses the numeric part of the port to derive a node ID.
// Example: :5003 → NodeID = 3
func (rm *ReplicationManager) extractNodeID(addr string) (int64, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return 0, err
	}
	return int64(p % 1000), nil
}

// isEligible returns whether a node with this ID is allowed to become leader.
// Here we assume: every follower that runs with --canBeLeader is eligible.
func (rm *ReplicationManager) isEligible(id int64) bool {

	return rm.canBeLeader
}

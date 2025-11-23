package nodes

import (
	"context"
	"log"
	"time"

	auction "tacoterror/grpc"

	"google.golang.org/grpc"
)

type ReplicationManager struct {
	node *Node

	followerAddrs []string
	leaderAddr    string
}

func NewReplicationManager(n *Node) *ReplicationManager {
	return &ReplicationManager{node: n}
}

func (rm *ReplicationManager) SetFollowers(addrs []string) {
	rm.followerAddrs = addrs
}

func (rm *ReplicationManager) SetLeader(addr string) {
	rm.leaderAddr = addr
}

// ----------------------
// Leader → Followers
// ----------------------

func (rm *ReplicationManager) ReplicateAuctionState(a *AuctionState) {
	if !rm.node.IsLeader {
		return
	}

	for _, addr := range rm.followerAddrs {
		faddr := addr
		go rm.sendReplicateBid(faddr, a)
	}
}

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

	_, err = client.ReplicateBid(ctx, &auction.ReplicateBidRequest{
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
}

// ----------------------
// Followers apply state
// ----------------------

func (rm *ReplicationManager) ApplyReplicatedBid(req *auction.ReplicateBidRequest) {
	rm.node.mu.Lock()
	defer rm.node.mu.Unlock()

	a, ok := rm.node.Auctions[req.AuctionId]
	if !ok {
		a = &AuctionState{
			AuctionID: req.AuctionId,
			EndTime:   time.Now().Add(rm.node.AuctionDuration),
		}
		rm.node.Auctions[req.AuctionId] = a
	}

	a.HighestBid = req.HighestBid
	a.Winner = req.Winner
	a.Status = req.Status
}

// ----------------------
// Follower → Leader Sync
// ----------------------

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

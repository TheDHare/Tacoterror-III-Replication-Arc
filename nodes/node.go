package nodes

import (
	"context"
	"sync"
	"time"

	auction "tacoterror/grpc"

	"google.golang.org/grpc"
)

/*
AuctionState represents the replicated state of a single auction,
including highest bid, winner, current status, timeout, and
a monotonically increasing sequence number used for ordering
replication updates.
*/
type AuctionState struct {
	AuctionID  int64
	HighestBid int64
	Winner     string
	Status     auction.AUCTION_STATUS
	EndTime    time.Time
	Sequence   int64
}

/*
Node represents a single process in the distributed auction system.
Nodes can operate either as leaders or followers. Leaders accept
client write operations, apply them locally, and replicate the new
state to all followers. Followers forward write operations to the
current leader and only update state through replication.
*/
type Node struct {
	mu sync.Mutex

	NodeID          int64
	IsLeader        bool
	LeaderID        int64
	AuctionDuration time.Duration

	ListenAddr string
	Auctions   map[int64]*AuctionState

	LastSequence   int64
	ReplicationMgr *ReplicationManager
}

/*
NewNode constructs a new node with the given identifier and auction
duration. Its leader/follower role is decided externally (main.go).
*/
func NewNode(id int64, duration time.Duration) *Node {
	return &Node{
		NodeID:          id,
		IsLeader:        false,
		LeaderID:        0,
		AuctionDuration: duration,
		Auctions:        make(map[int64]*AuctionState),
		LastSequence:    0,
	}
}

/*
HandleBid processes a client request to place a bid.
  - Followers forward the bid to the leader.
  - Leaders validate and apply the bid, increasing the sequence number,
    and then replicate the updated state to followers.
*/
func (n *Node) HandleBid(ctx context.Context, req *auction.BidRequest) (*auction.BidReply, error) {
	if !n.IsLeader {
		return n.forwardBidToLeader(ctx, req)
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	a, exists := n.Auctions[req.AuctionId]
	if !exists {
		a = &AuctionState{
			AuctionID:  req.AuctionId,
			Status:     auction.AUCTION_STATUS_ONGOING,
			EndTime:    time.Now().Add(n.AuctionDuration),
			Sequence:   0,
			HighestBid: 0,
			Winner:     "",
		}
		n.Auctions[req.AuctionId] = a
	}

	if time.Now().After(a.EndTime) && a.Status == auction.AUCTION_STATUS_ONGOING {
		a.Status = auction.AUCTION_STATUS_FINISHED
	}

	if a.Status == auction.AUCTION_STATUS_FINISHED {
		return &auction.BidReply{
			BiddingStatus: auction.BIDDING_STATUS_FAIL,
			AuctionId:     req.AuctionId,
			HighestBid:    a.HighestBid,
			Reason:        "auction finished",
		}, nil
	}

	if req.Amount <= a.HighestBid {
		return &auction.BidReply{
			BiddingStatus: auction.BIDDING_STATUS_FAIL,
			AuctionId:     req.AuctionId,
			HighestBid:    a.HighestBid,
			Reason:        "bid too low",
		}, nil
	}

	n.LastSequence++
	a.Sequence = n.LastSequence
	a.HighestBid = req.Amount
	a.Winner = req.BidderName

	n.ReplicationMgr.ReplicateAuctionState(a)

	return &auction.BidReply{
		BiddingStatus: auction.BIDDING_STATUS_SUCCESS,
		AuctionId:     req.AuctionId,
		HighestBid:    a.HighestBid,
		Reason:        "ok",
	}, nil
}

/*
forwardBidToLeader sends a bid request to the leader when this node
is operating as a follower. The leader processes the bid and returns
the authoritative response.
*/
func (n *Node) forwardBidToLeader(ctx context.Context, req *auction.BidRequest) (*auction.BidReply, error) {
	conn, err := grpc.DialContext(ctx, n.ReplicationMgr.LeaderAddr(), grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return &auction.BidReply{
			BiddingStatus: auction.BIDDING_STATUS_FAIL,
			AuctionId:     req.AuctionId,
			HighestBid:    0,
			Reason:        "leader unreachable",
		}, nil
	}
	defer conn.Close()

	client := auction.NewAuctionServiceClient(conn)
	return client.Bid(ctx, req)
}

/*
HandleResult returns the current observable state of an auction,
whether the auction is ongoing or finished.
*/
func (n *Node) HandleResult(ctx context.Context, req *auction.ResultRequest) (*auction.ResultReply, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	a, exists := n.Auctions[req.AuctionId]
	if !exists {
		return &auction.ResultReply{
			Status:            auction.AUCTION_STATUS_ONGOING,
			AuctionId:         req.AuctionId,
			CurrentHighestBid: 0,
			AuctionWinner:     "",
		}, nil
	}

	if time.Now().After(a.EndTime) && a.Status == auction.AUCTION_STATUS_ONGOING {
		a.Status = auction.AUCTION_STATUS_FINISHED
	}

	return &auction.ResultReply{
		Status:            a.Status,
		AuctionId:         a.AuctionID,
		CurrentHighestBid: a.HighestBid,
		AuctionWinner:     a.Winner,
	}, nil
}

/*
HandleReplicateBid applies a state update received from the leader.
Sequence numbers ensure ordering and idempotency. Followers never
modify auction state outside replication.
*/
func (n *Node) HandleReplicateBid(ctx context.Context, req *auction.ReplicateBidRequest) (*auction.ReplicateBidReply, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	a, exists := n.Auctions[req.AuctionId]
	if !exists {
		a = &AuctionState{
			AuctionID: req.AuctionId,
			EndTime:   time.Now().Add(n.AuctionDuration),
		}
		n.Auctions[req.AuctionId] = a
	}

	if req.Sequence <= a.Sequence {
		return &auction.ReplicateBidReply{
			Success:             false,
			Reason:              "outdated sequence",
			LastAppliedSequence: a.Sequence,
		}, nil
	}

	a.HighestBid = req.HighestBid
	a.Winner = req.Winner
	a.Status = req.Status
	a.Sequence = req.Sequence

	if req.Sequence > n.LastSequence {
		n.LastSequence = req.Sequence
	}

	return &auction.ReplicateBidReply{
		Success:             true,
		Reason:              "replicated",
		LastAppliedSequence: a.Sequence,
	}, nil
}

/*
HandleSyncState returns the authoritative auction state to a follower
performing recovery or late-join synchronization.
*/
func (n *Node) HandleSyncState(ctx context.Context, req *auction.SyncStateRequest) (*auction.SyncStateReply, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	a, exists := n.Auctions[req.AuctionId]
	if !exists {
		return &auction.SyncStateReply{
			AuctionId:     req.AuctionId,
			HighestBid:    0,
			AuctionWinner: "",
			Status:        auction.AUCTION_STATUS_ONGOING,
			LastSequence:  0,
		}, nil
	}

	return &auction.SyncStateReply{
		AuctionId:     a.AuctionID,
		HighestBid:    a.HighestBid,
		AuctionWinner: a.Winner,
		Status:        a.Status,
		LastSequence:  a.Sequence,
	}, nil
}

/*
SyncAllAuctionsFromLeader performs recovery by requesting the latest
state for every known auction from the current leader.
*/
func (n *Node) SyncAllAuctionsFromLeader() {
	if n.ReplicationMgr.LeaderAddr() == "" {
		return
	}

	for id := range n.Auctions {
		_ = n.ReplicationMgr.SyncAuctionFromLeader(context.Background(), id)
	}
}

package server

import (
	"context"

	auction "tacoterror/grpc"
	"tacoterror/nodes"
)

type AuctionServer struct {
	auction.UnimplementedAuctionServiceServer
	auction.UnimplementedReplicationServiceServer

	node *nodes.Node
}

func NewAuctionServer(n *nodes.Node) *AuctionServer {
	return &AuctionServer{node: n}
}

// API RPCs
func (s *AuctionServer) Bid(ctx context.Context, req *auction.BidRequest) (*auction.BidReply, error) {
	return s.node.HandleBid(ctx, req)
}

func (s *AuctionServer) Result(ctx context.Context, req *auction.ResultRequest) (*auction.ResultReply, error) {
	return s.node.HandleResult(ctx, req)
}

// Internal replication RPCs
func (s *AuctionServer) ReplicateBid(ctx context.Context, req *auction.ReplicateBidRequest) (*auction.ReplicateBidReply, error) {
	return s.node.HandleReplicateBid(ctx, req)

}

func (s *AuctionServer) SyncState(ctx context.Context, req *auction.SyncStateRequest) (*auction.SyncStateReply, error) {
	return s.node.HandleSyncState(ctx, req)
}

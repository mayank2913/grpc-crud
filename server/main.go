package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	userpb "../proto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UserServiceServer struct {
}

func (s *UserServiceServer) CreateUser(ctx context.Context, req *userpb.CreateUserReq) (*userpb.CreateUserRes, error) {

	user := req.GetUser()
	data := userItem{
		UserID: user.GetAuthorId(),
	}

	result, err := userdb.InsertOne(mongoCtx, data)

	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			fmt.Sprintf("Internal error: %v", err),
		)
	}

	oid := result.InsertedID.(primitive.ObjectID)
	user.Id = oid.Hex()

	return &userpb.CreateUserRes{User: user}, nil
}

func (s *UserServiceServer) ReadUser(ctx context.Context, req *userpb.ReadUserReq) (*userpb.ReadUserRes, error) {

	oid, err := primitive.ObjectIDFromHex(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("Could not convert to ObjectId: %v", err))
	}
	result := userdb.FindOne(ctx, bson.M{"_id": oid})

	data := userItem{}

	if err := result.Decode(&data); err != nil {
		return nil, status.Errorf(codes.NotFound, fmt.Sprintf("Could not find user with Object Id %s: %v", req.GetId(), err))
	}

	response := &userpb.ReadUserRes{
		User: &userpb.User{
			Id: oid.Hex(),
		},
	}
	return response, nil
}

func (s *UserServiceServer) UpdateUser(ctx context.Context, req *userpb.UpdateUserReq) (*userpb.UpdateUserRes, error) {

	user := req.GetUser()

	oid, err := primitive.ObjectIDFromHex(user.GetId())
	if err != nil {
		return nil, status.Errorf(
			codes.InvalidArgument,
			fmt.Sprintf("Could not convert the supplied user id to a MongoDB ObjectId: %v", err),
		)
	}

	update := bson.M{
		"authord_id": user.GetAuthorId(),
		"title":      user.GetTitle(),
		"content":    user.GetContent(),
	}

	filter := bson.M{"_id": oid}

	result := userdb.FindOneAndUpdate(ctx, filter, bson.M{"$set": update}, options.FindOneAndUpdate().SetReturnDocument(1))

	decoded := userItem{}
	err = result.Decode(&decoded)
	if err != nil {
		return nil, status.Errorf(
			codes.NotFound,
			fmt.Sprintf("Could not find user with supplied ID: %v", err),
		)
	}
	return &userpb.UpdateUserRes{
		User: &userpb.User{
			Id:     decoded.ID.Hex(),
			UserId: decoded.UserID,
		},
	}, nil
}

func (s *UserServiceServer) DeleteUser(ctx context.Context, req *userpb.DeleteUserReq) (*userpb.DeleteUserRes, error) {
	oid, err := primitive.ObjectIDFromHex(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("Could not convert to ObjectId: %v", err))
	}

	_, err = userdb.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return nil, status.Errorf(codes.NotFound, fmt.Sprintf("Could not find/delete user with id %s: %v", req.GetId(), err))
	}
	return &userpb.DeleteUserRes{
		Success: true,
	}, nil
}

func (s *UserServiceServer) ListUsers(req *userpb.ListUsersReq, stream userpb.UserService_ListUsersServer) error {

	data := &userItem{}

	cursor, err := userdb.Find(context.Background(), bson.M{})
	if err != nil {
		return status.Errorf(codes.Internal, fmt.Sprintf("Unknown internal error: %v", err))
	}

	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {

		err := cursor.Decode(data)
		// check error
		if err != nil {
			return status.Errorf(codes.Unavailable, fmt.Sprintf("Could not decode data: %v", err))
		}

		stream.Send(&userpb.ListUsersRes{
			User: &userpb.User{
				Id:     data.ID.Hex(),
				UserId: data.UserID,
			},
		})
	}

	if err := cursor.Err(); err != nil {
		return status.Errorf(codes.Internal, fmt.Sprintf("Unkown cursor error: %v", err))
	}
	return nil
}

type userItem struct {
	ID     primitive.ObjectID `bson:"_id,omitempty"`
	UserID string             `bson:"author_id"`
}

var db *mongo.Client
var userdb *mongo.Collection
var mongoCtx context.Context

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	fmt.Println("Starting server on port :50051...")
	listener, err := net.Listen("tcp", ":50051")

	if err != nil {
		log.Fatalf("Unable to listen on port :50051: %v", err)
	}

	opts := []grpc.ServerOption{}
	s := grpc.NewServer(opts...)
	srv := &UserServiceServer{}
	userpb.RegisterUserServiceServer(s, srv)

	fmt.Println("Connecting to MongoDB...")
	mongoCtx = context.Background()
	db, err = mongo.Connect(mongoCtx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	err = db.Ping(mongoCtx, nil)
	if err != nil {
		log.Fatalf("Couldn't connect to MongoDB: %v\n", err)
	} else {
		fmt.Println("Connected to Mongodb")
	}

	userdb = db.Database("mydb").Collection("user")

	go func() {
		if err := s.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()
	fmt.Println("Server succesfully started on port :50051")

	c := make(chan os.Signal)

	signal.Notify(c, os.Interrupt)

	<-c

	fmt.Println("\nStopping the server...")
	s.Stop()
	listener.Close()
	fmt.Println("Closing mongo connection")
	db.Disconnect(mongoCtx)
}

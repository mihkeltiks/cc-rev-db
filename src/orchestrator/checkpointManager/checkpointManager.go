package checkpointmanager

import (
	"fmt"
	"strconv"

	"logger"
	"rpc"
	"utils/mpi"
)

type NodeId int

type checkpointRecord struct {
	Id              string
	nodeId          NodeId
	NodeRank        *int
	OpName          string
	IsSend          bool
	CanBeRestored   bool
	parameters      map[string]string
	MatchingEventId *string
	matchingEvent   *checkpointRecord // for send events, a link to the corresponding message receive event, and vice versa
	Tag             *int              // The mpi message tag, if present
	CurrentLocation bool
}

type CheckpointLog map[NodeId][]*checkpointRecord

// Data structure for maintaining a list of recorded checkpoints by node
var checkpointLog = make(CheckpointLog)

func GetCheckpointLog() CheckpointLog {
	return checkpointLog
}

var nodeRanks = make(map[NodeId]*int)

func RecordCheckpoint(mpiRecord rpc.MPICallRecord) {
	nodeId := NodeId(mpiRecord.NodeId)
	opName := mpiRecord.OpName

	record := checkpointRecord{
		Id:            mpiRecord.Id,
		nodeId:        nodeId,
		OpName:        opName,
		IsSend:        mpi.SEND_EVENTS[opName],
		CanBeRestored: mpi.RESTORABLE_OPERATIONS[opName],
		parameters:    mpiRecord.Parameters,
	}

	if nodeRanks[nodeId] == nil {
		nodeRanks[nodeId] = tryEvaluateIntegerParam("rank", record)
	}
	record.NodeRank = nodeRanks[nodeId]

	record.Tag = tryEvaluateIntegerParam("tag", record)

	// Link the matching event from other party, if already recorded
	record.findAndLinkMatchingMessage()

	if checkpointLog[nodeId] == nil {
		checkpointLog[nodeId] = make([]*checkpointRecord, 0)
	}

	checkpointLog[nodeId] = append(checkpointLog[nodeId], &record)
}

func findCheckpointById(checkpointId string) *checkpointRecord {
	for _, nodeCheckpoints := range checkpointLog {
		for _, checkpoint := range nodeCheckpoints {
			if checkpoint.Id == checkpointId {
				return checkpoint
			}
		}
	}
	return nil
}

func ListCheckpoints() {
	for nodeId, nodeCheckpoints := range checkpointLog {
		var str string

		for _, record := range nodeCheckpoints {
			str = fmt.Sprintf("%s{%s - %s}", str, record.OpName, record.Id)
			str = fmt.Sprintf("%s,", str)
		}

		logger.Info("Node %d checkpoints:", nodeId)
		logger.Info(str)
	}
}

// Links a corresponding send event to receive events, and vice versa, if found
func (record *checkpointRecord) findAndLinkMatchingMessage() {
	var matchingRecord *checkpointRecord

	switch record.OpName {
	case mpi.MPI_OPS[mpi.OP_SEND]:
		matchingNodeRank, _ := strconv.Atoi(record.parameters["dest"])

		matchingRecord = getFirstUnmatchedMessage(matchingNodeRank, mpi.MPI_OPS[mpi.OP_RECV], record.Tag)

	case mpi.MPI_OPS[mpi.OP_RECV]:
		matchingNodeRank, _ := strconv.Atoi(record.parameters["source"])

		matchingRecord = getFirstUnmatchedMessage(matchingNodeRank, mpi.MPI_OPS[mpi.OP_SEND], record.Tag)
	}

	if matchingRecord != nil {
		logger.Verbose("Linking matching messages  %v:%v - %v:%v", record.nodeId, record.OpName, matchingRecord.nodeId, matchingRecord.OpName)
		record.matchingEvent = matchingRecord
		record.MatchingEventId = &matchingRecord.Id

		matchingRecord.matchingEvent = record
		matchingRecord.MatchingEventId = &record.Id
	}

}

// Finds the first message on a node with specified operation name
func getFirstUnmatchedMessage(nodeRank int, opName string, tag *int) *checkpointRecord {
	var nodeId *NodeId

	for nId, nRank := range nodeRanks {
		if nRank != nil && *nRank == nodeRank {
			nodeId = &nId
			break
		}
	}

	if nodeId == nil {
		return nil
	}

	nodeCheckpoints := checkpointLog[*nodeId]
	if nodeCheckpoints == nil {
		return nil
	}

	for _, checkpoint := range nodeCheckpoints {
		if checkpoint.matchingEvent != nil || checkpoint.CurrentLocation {
			continue
		}
		if checkpoint.OpName == opName && tagsMatch(tag, checkpoint.Tag) {
			return checkpoint
		}
	}
	return nil
}

func tagsMatch(tag1, tag2 *int) bool {
	// tag retrieval has failed, might be false positive
	if tag1 == nil || tag2 == nil {
		return true
	}

	// wildcard tag used
	if *tag1 == -1 || *tag2 == -1 {
		return true
	}

	// matching tags used
	return *tag1 == *tag2
}

func tryEvaluateIntegerParam(paramName string, record checkpointRecord) *int {
	paramStr := record.parameters[paramName]
	if len(paramStr) == 0 {
		return nil
	}

	if value, err := strconv.Atoi(paramStr); err == nil {
		return &value
	}

	return nil
}

func RemoveSubsequentCheckpoints(cpoint checkpointRecord) {
	for nodeIndex, nodeCheckpoints := range checkpointLog {
		for cpIndex, checkpoint := range nodeCheckpoints {
			if checkpoint.Id == cpoint.Id {
				checkpointLog[nodeIndex] = checkpointLog[nodeIndex][:cpIndex+1]
				if cpoint.matchingEvent != nil {
					checkpointLog[nodeIndex][cpIndex].matchingEvent = nil
					checkpointLog[nodeIndex][cpIndex].MatchingEventId = nil
				}
				checkpointLog[nodeIndex][cpIndex].CurrentLocation = true
				return
			}
		}
	}
}

func RemoveCurrentCheckpointMarkersOnNode(nodeId NodeId) {
	for _, checkpoint := range checkpointLog[nodeId] {
		if checkpoint.CurrentLocation {
			checkpoint.CurrentLocation = false
			checkpoint.findAndLinkMatchingMessage()
		}
	}
}

func (c checkpointRecord) String() string {
	return fmt.Sprintf("%v - %v", c.Id, c.OpName)
}

package main

//lint:file-ignore U1000 ignore unused helpers

import (
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/ottmartens/cc-rev-db/debugger/dwarf"
	"github.com/ottmartens/cc-rev-db/logger"
	"github.com/ottmartens/cc-rev-db/rpc"
)

const MAIN_FN = "main"

var cliMode = false
var orchestratorAddress string
var nodeId int

type processContext struct {
	targetFile     string           // the executing binary file
	sourceFile     string           // source code file
	dwarfData      *dwarf.DwarfData // dwarf debug information about the binary
	process        *exec.Cmd        // the running binary
	pid            int              // the process id of the running binary
	bpointData     breakpointData   // holds the instuctions for currently replaced by breakpoints
	cpointData     checkpointData   // holds data about currently recorded checkppoints
	checkpointMode CheckpointMode   // whether checkpoints are recorded in files or in forked processes
}

func main() {
	// As ptrace calls depend on per-thread state, we must lock the thread
	runtime.LockOSThread()

	precleanup()

	targetFile, checkpointMode, orchestratorAddress := getValuesFromArgs()

	ctx := &processContext{
		targetFile:     targetFile,
		checkpointMode: checkpointMode,
		bpointData:     breakpointData{}.New(),
		cpointData:     checkpointData{}.New(),
	}

	if !cliMode {
		// connect to orchestrator
		rpc.Client.Connect(orchestratorAddress)

		nodeId = rpc.Client.ReportAsHealthy()
		logger.SetSendRemoteLog(rpc.Client.SendLog, nodeId)

		logger.Info("Process (pid: %d) registered", os.Getpid())
	}

	// parse debugging data
	ctx.dwarfData = dwarf.ParseDwarfData(ctx.targetFile)
	ctx.dwarfData.ResolveMPIDebugInfo(MPI_FUNCS.SIGNATURE)
	ctx.sourceFile = ctx.dwarfData.FindEntrySourceFile(MAIN_FN)

	// start target binary
	ctx.process = startBinary(ctx.targetFile)
	ctx.pid = ctx.process.Process.Pid

	// set up automatic breakpoints
	insertMPIBreakpoints(ctx)

	if cliMode {
		handleCLIWorkflow(ctx)
	} else {
		handleRemoteWorkflow(ctx)
	}

	logger.Info("False")
	runtime.UnlockOSThread()
}

func handleRemoteWorkflow(ctx *processContext) {
	logger.Verbose("Registering debugging methods for remote use")

	port := 3500 + nodeId

	rpc.InitializeServer(port, func(register rpc.Registrator) {
		logger.Info("registering smthing")
	})
}

func handleCLIWorkflow(ctx *processContext) {
	printInstructions()

	for {
		cmd := askForInput()

		res := cmd.handle(ctx)

		if res.exited { // binary exited
			break
		}
	}
}

func startBinary(target string) *exec.Cmd {
	time.Sleep(time.Second * time.Duration(rand.Float32()))

	cmd := exec.Command(target)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Ptrace: true,
	}

	cmd.Start()

	err := cmd.Wait()

	if err != nil {
		// arrived at auto-inserted initial breakpoint trap
		logger.Debug("child: %v", err)
		logger.Info("binary started, waiting for command")
	}

	must(syscall.PtraceSetOptions(cmd.Process.Pid, syscall.PTRACE_O_TRACECLONE))

	return cmd
}

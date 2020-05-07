package main

import (
    "flag"
    "github.com/ngaut/log"
    "tendisplus/integrate_test/util"
    "strconv"
    "time"
    "os/exec"
    "fmt"
    "strings"
    "github.com/mediocregopher/radix.v2/redis"
)

func getNodeName(m *util.RedisServer) string {
    cmd := exec.Command("../../../bin/redis-cli", "-h", m.Ip, "-p", strconv.Itoa(m.Port),
        "-a", *auth,
        "cluster", "myid")
    output, err := cmd.Output()
    //fmt.Print(string(output))
    if err != nil {
        fmt.Print(err)
    }
    log.Infof("getNodeName sucess. %s:%d nodename:%s", m.Ip, m.Port, output)
    return strings.Trim(string(output), "\n");
}

func printNodes(m *util.RedisServer) {
    cmd := exec.Command("../../../bin/redis-cli", "-h", m.Ip, "-p", strconv.Itoa(m.Port),
        "-a", *auth,
        "cluster", "nodes")
    output, err := cmd.Output()
    //fmt.Print(string(output))
    if err != nil {
        fmt.Print(err)
    }
    log.Infof("printNodes sucess. %s:%d nodes:%s", m.Ip, m.Port, output)
}

func addDataByClientInCoroutine(m *util.RedisServer, num int, prefixkey string, channel chan int) {
    addDataByClient(m, num, prefixkey)
    channel <- 0
}

func addDataByClient(m *util.RedisServer, num int, prefixkey string) {
    log.Infof("addData begin. %s:%d", m.Ip, m.Port)
    // TODO(takenliu):mset dont support moved ???
    for i := 0; i < num; i++ {
        var args []string
        args = append(args,  "-h", m.Ip, "-p", strconv.Itoa(m.Port),
            "-c", "-a", *auth, "set")

        key := "key" + prefixkey + "_" + strconv.Itoa(i)
        value := "value" + prefixkey + "_" + strconv.Itoa(i)
        args = append(args, key)
        args = append(args, value)

        cmd := exec.Command("../../../bin/redis-cli", args...)
        //cmd := exec.Command("../../../bin/redis-cli", "-h", m.Ip, "-p", strconv.Itoa(m.Port),
        //    "-c", "-a", *auth, "mset", kvs...)
        data, err := cmd.Output()
        //fmt.Print(string(output))
        if string(data) != "OK\n" || err != nil {
            log.Infof("set failed, key:%v data:%s err:%v", key, data, err)
        }
    }
    log.Infof("addData sucess. %s:%d num:%d", m.Ip, m.Port, num)
}

func checkDataInCoroutine(m *[]util.RedisServer, num int, prefixkey string, channel chan int) {
    checkData(m, num, prefixkey)
    channel <- 0
}

func checkData(m *[]util.RedisServer, num int, prefixkey string) {
    log.Infof("checkData begin. prefixkey:%s", prefixkey)

    for i := 0; i < num; i++ {
        // redis-cli will process moved station in get command
        var args []string
        args = append(args,  "-h", (*m)[0].Ip, "-p", strconv.Itoa((*m)[0].Port),
            "-c", "-a", *auth, "get")

        key := "key" + prefixkey + "_" + strconv.Itoa(i)
        value := "value" + prefixkey + "_" + strconv.Itoa(i)
        args = append(args, key)

        cmd := exec.Command("../../../bin/redis-cli", args...)
        data, err := cmd.Output()

        retValue := strings.Replace(string(data), "\n", "", -1)
        if retValue != value {
            log.Infof("find failed, key:%v data:%s value:%s err:%v", key, retValue, value, err)
        }
    }
    log.Infof("checkData end. prefixkey:%s", prefixkey)
}

var (
    CLUSTER_SLOTS = 16384
)
type NodeInfo struct {
    index int
    startSlot int
    endSlot int
    migrateStartSlot int
    migrateEndSlot int
}

func testCluster(clusterIp string, clusterPortStart int, clusterNodeNum int) {
    var nodeInfoArray []NodeInfo
    perNodeMigrateNum := CLUSTER_SLOTS / (clusterNodeNum+1) /clusterNodeNum
    for i := 0; i <= clusterNodeNum; i++ {
        var startSlot = CLUSTER_SLOTS / clusterNodeNum * i;
        var endSlot = startSlot + CLUSTER_SLOTS / clusterNodeNum - 1;
        if i == (clusterNodeNum - 1) {
            endSlot = CLUSTER_SLOTS - 1;
        }
        var migrateStart = endSlot - perNodeMigrateNum
        if migrateStart <= startSlot{
            migrateStart = startSlot
        }
        migrateEnd := endSlot
        nodeInfoArray = append(nodeInfoArray,
            NodeInfo{i, startSlot, endSlot, migrateStart, migrateEnd})
    }

    pwd := getCurrentDirectory()
    log.Infof("current pwd:" + pwd)
    kvstorecount := 2

    log.Infof("start servers clusterNodeNum:%d", clusterNodeNum)
    var servers []util.RedisServer
    dstNodeIndex := clusterNodeNum
    // migrate from node[0, clusterNodeNum-1] to node[clusterNodeNum]
    for i := 0; i <= clusterNodeNum; i++ {
        server := util.RedisServer{}
        port := clusterPortStart + i
        server.Init(clusterIp, port, pwd, "m" + strconv.Itoa(i) + "_")
        cfgArgs := make(map[string]string)
        cfgArgs["maxBinlogKeepNum"] = "100"
        cfgArgs["kvstorecount"] = strconv.Itoa(kvstorecount)
        cfgArgs["cluster-enabled"] = "true"
        cfgArgs["pauseTimeIndexMgr"] = "1"
        cfgArgs["rocks.blockcachemb"] = "24"
        cfgArgs["requirepass"] = "tendis+test"
        cfgArgs["masterauth"] = "tendis+test"
        cfgArgs["generalLog"] = "true"
        if err := server.Setup(false, &cfgArgs); err != nil {
            log.Fatalf("setup failed,port:%s err:%v", port, err)
        }
        servers = append(servers, server)
    }
    time.Sleep(2 * time.Second)

    // meet
    log.Infof("cluster meet begin")
    cli0 := createClient(&servers[0])
    for i := 1; i < clusterNodeNum; i++ {
        if r, err := cli0.Cmd("cluster", "meet", servers[i].Ip, servers[i].Port).Str();
            r != ("OK") {
            log.Fatalf("meet failed:%v %s", err, r)
            return
        }
    }

    // add slot
    log.Infof("cluster addslots begin")
    for i := 0; i < clusterNodeNum; i++ {
        cli := createClient(&servers[i])
        slots := "{" + strconv.Itoa(nodeInfoArray[i].startSlot) + ".." + strconv.Itoa(nodeInfoArray[i].endSlot) + "}"
        log.Infof("addslot %d %s", i, slots)
        if r, err := cli.Cmd("cluster", "addslots", slots).Str();
            r != ("OK") {
            log.Fatalf("meet failed:%v %s", err, r)
            return
        }
    }

    time.Sleep(10 * time.Second)

    // add data
    log.Infof("cluster add data begin")
    var channel chan int = make(chan int)
    for i := 0; i < clusterNodeNum; i++ {
        // go addDataInCoroutine(&servers[i], *num1, "{12}", channel)
        go addDataByClientInCoroutine(&servers[i], *num1, strconv.Itoa(i), channel)
    }

    // add the dst node
    log.Infof("cluster meet the dst node")
    if r, err := cli0.Cmd("cluster", "meet", servers[dstNodeIndex].Ip, servers[dstNodeIndex].Port).Str();
        r != ("OK") {
        log.Fatalf("meet failed:%v %s", err, r)
        return
    }
    time.Sleep(10 * time.Second)

    // migrate
    log.Infof("cluster migrate begin")
    cliDst := createClient(&servers[dstNodeIndex])
    dstNodeName := getNodeName(&servers[dstNodeIndex])
    log.Infof("cluster migrate dstNodeName:%s perNodeMigrateNum:%d", dstNodeName, perNodeMigrateNum)
    for i := 0; i < clusterNodeNum; i++ {
        printNodes(&servers[i])
        srcNodeName := getNodeName(&servers[i])
        //cli := createClient(&servers[i])

        var slots []int
        for j := nodeInfoArray[i].migrateStartSlot; j <= nodeInfoArray[i].migrateEndSlot; j++ {
            slots = append(slots, j)
        }
        log.Infof("migrate node:%d srcNodename:%s slots:%v", i, srcNodeName, slots)
        /*if r, err := cli.Cmd("cluster", "setslot", "migrating", dstNodeName, slots).Str();
            r != ("OK") {
            log.Fatalf("migrating failed:%v %s", err, r)
            return
        }
        time.Sleep(1 * time.Second)*/
        if r, err := cliDst.Cmd("cluster", "setslot", "importing", srcNodeName, slots).Str();
            r != ("OK") {
            log.Fatalf("importing failed:%v %s", err, r)
            return
        }
    }

    // wait addDataInCoroutine
    log.Infof("cluster add data end")
    for i := 0; i < clusterNodeNum; i++ {
        <- channel
    }

    time.Sleep(30 * time.Second)
    // check slots
    {
        ret := cli0.Cmd("cluster", "slots")
        log.Infof("checkSlotsInfo0 :%s", ret)
        if (!ret.IsType(redis.Array)) {
            log.Fatalf("cluster slots failed:%v", ret)
        }
        ret_array, _ := ret.Array()
        if len(ret_array) != clusterNodeNum*2 {
            log.Fatalf("cluster slots size not right:%v", ret_array)
        }
        for _,value := range ret_array {
            log.Infof("checkSlotsInfo1 :%s", value)
            if !value.IsType(redis.Array) {
                log.Fatalf("cluster slots data not array:%v", value)
            }
            ret_array2, _ := value.Array()
            if len(ret_array2) != 3 ||
                !ret_array2[0].IsType(redis.Int) ||
                !ret_array2[1].IsType(redis.Int) ||
                !ret_array2[2].IsType(redis.Array) {
                log.Fatalf("cluster slots value not right:%v", ret_array2)
            }
            startSlot,_ := ret_array2[0].Int()
            endSlot,_ := ret_array2[1].Int()
            ret_array3, _ := ret_array2[2].Array()
            if len(ret_array3) != 3 ||
                !ret_array3[0].IsType(redis.Str) ||
                !ret_array3[1].IsType(redis.Int) ||
                !ret_array3[0].IsType(redis.Str){
                log.Fatalf("cluster slots value not right:%v", ret_array3)
            }
            ip,_ := ret_array3[0].Str()
            port,_ := ret_array3[1].Int()
            nodeName,_ := ret_array3[2].Str()
            log.Infof("startslot:%v endslot:%v ip:%v port:%v nodename:%v", startSlot, endSlot, ip, port, nodeName)
            nodeIndex := port - clusterPortStart
            // check src nodes
            if nodeIndex < clusterNodeNum &&
                (startSlot != nodeInfoArray[nodeIndex].startSlot ||
                endSlot != nodeInfoArray[nodeIndex].migrateStartSlot - 1) {
                log.Fatalf("cluster slots not right,startSlot:%v endSlot:%v cluster slots:%v",
                    nodeInfoArray[nodeIndex].startSlot, nodeInfoArray[nodeIndex].migrateStartSlot-1,
                    ret_array2)
            }
            // TOD(takenliu): check dst node
        }
    }

    // check keys num
    totalKeyNum := 0
    for i := 0; i <= clusterNodeNum; i++ {
        cli := createClient(&servers[i])
        nodeKeyNum := 0
        for j := 0; j < CLUSTER_SLOTS; j++ {
            r, err := cli.Cmd("cluster", "countkeysinslot", j).Int();
            if err != nil {
                log.Fatalf("cluster countkeysinslot failed:%v %s", err, r)
                return
            }
            log.Infof("cluster countkeysinslot, server:%d slot:%d num:%d", i, j, r)
            nodeKeyNum += r
            // check src node migrated slot key num should be 0
            if i < clusterNodeNum && j < nodeInfoArray[i].startSlot && r != 0 {
                log.Fatalf("check keys num failed,server:%v slot:%v keynum:%v",
                    i, j, r)
            }
            if i < clusterNodeNum && j >= nodeInfoArray[i].migrateStartSlot && r != 0 {
                log.Fatalf("check keys num failed,server:%v slot:%v keynum:%v",
                    i, j, r)
            }
            // TODO(takenliu): check dst node
        }
        log.Infof("check keys num server:%d keynum:%d", i, nodeKeyNum)
        totalKeyNum += nodeKeyNum
    }
    log.Infof("check keys num totalkeynum:%d", totalKeyNum)
    if totalKeyNum != clusterNodeNum * *num1 {
        for i := 0; i < clusterNodeNum; i++ {
            go checkDataInCoroutine(&servers, *num1, strconv.Itoa(i), channel)
        }
        for i := 0; i < clusterNodeNum; i++ {
            <- channel
        }
        log.Fatalf("check keys num failed:%d != %d", totalKeyNum, clusterNodeNum * *num1)
    }

    for i := 0; i <= clusterNodeNum; i++ {
        //shutdownServer(&servers[i], *shutdown, *clear);
    }
}

func main(){
    flag.Parse()
    // rand.Seed(time.Now().UTC().UnixNano())
    testCluster(*clusterIp, *clusterPortStart, *clusterNodeNum)
}
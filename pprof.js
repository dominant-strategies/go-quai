const readline = require('readline');
const events = require('events');
const axios = require('axios');
const eventEmitter = new events.EventEmitter();

// configuration
let targetDuration = 2 * 1000; // 2 seconds converted to milliseconds
let actionThreshold = 2;  // number of times log needs to occur to trigger action
let timeFrame =  10 * 1000; // 1 minute timeframe in milliseconds

let logCount = 0;
let bucket = [];

const ports = ["8081","8090","8100","8110","8091","8092","8093","8101","8102","8103","8111","8112","8113"]
const names = ["prime","cyprus","paxos","hydra","cyprus1","cyprus2","cyprus3","paxos1","paxos2","paxos3","hydra1","hydra2","hydra3"]
const pprofs = ["heap", "goroutine", "block", "mutex"]

// Function to trigger when log count hits the threshold
async function actionToPerform() {
    for (let i = 0; i < names.length; i++) {
      const name = names[i]
      const port = ports[i]
      for (pprof of pprofs) {
        try {
            const response = await axios.get(`http://localhost:${port}/debug/pprof/${pprof}`);
            fs.writeFileSync(`${name}-${Date.now()}-${pprof}.pb.gz`, response.data, 'utf8');
  
        } catch (error) {
            console.error('Request Failed:', error);
        }
      }
    }
    logCount = 0;
    bucket = [];
}

// Set up a readline interface from process.stdin
const rl = readline.createInterface({
    input: process.stdin,
    output: process.stdout,
    terminal: false
});

// Whenever a new line is read
rl.on('line', (line) => {
    // Parse the line
    const parts = line.split(',');
    const durationStr = parts[3];
    let duration;

    // Extract the numeric value and the unit (s or ms)
    const match = durationStr.match(/([0-9.]+)(ms|s)/);

    if (match) {
        // Convert the duration to milliseconds
        duration = parseFloat(match[1]);
        if (match[2] === 's') {
            duration *= 1000;
        }

        // Check if duration is greater than target duration
        if (duration > targetDuration) {
            logCount++;
            bucket.push({log: line, timestamp: Date.now()});

            // Check if the count has reached the threshold
            if (logCount >= actionThreshold) {
                actionToPerform();
            }
        }
    }

    // Check if the timeframe has passed for the first log in the bucket
    if (bucket.length > 0 && (Date.now() - bucket[0].timestamp > timeFrame)) {
        bucket.shift();
        logCount--;
    }
});

// Handle 'end' event
rl.on('close', () => {
    console.log('Stream ended');
    process.exit(0);
});

// Handle 'error' event
rl.on('error', (err) => {
    console.error(`Error encountered: ${err}`);
    process.exit(1);
});


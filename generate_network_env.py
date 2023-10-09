import json, os

# PORTS ARE ORDERED LIKE THIS:
# TCP, HTTP, WS

def TCP(port):
    return port
def HTTP(port):
    return port + 1
def WS(port):
    return port + 2

def coinbase_address(region_id, zone_id):
    return f'0x{hex(region_id)[-1]}{hex(zone_id)[-1]}00000000000000000000000000000000000001'

"""
def generate_network_env(input_json):
    with open(input_json, 'r') as f:
        data = json.load(f)
        
        prime_port = data["initial_prime_port"]
        region_port = data["initial_region_port"]
        zone_port = data["initial_zone_port"]
        width = data["width"]

    output = []

    # Generate ZONE_x_y_COINBASE addresses
    output.append("# Unique Coinbase addresses")
    coinbase_addresses = []
    for i in range(width):
        for j in range(width):
            coinbase_addresses.append(f"ZONE_{i}_{j}_COINBASE={coinbase_address(i, j)}")
    output.append("\n".join(coinbase_addresses))
    output.append("")

    # Prime ports
    output.append("# Ports (TCP/UCP), HTTP, WS")
    output.append(f"PRIME_PORT_TCP={TCP(prime_port)}")
    output.append(f"PRIME_PORT_HTTP={HTTP(prime_port)}")
    output.append(f"PRIME_PORT_WS={WS(prime_port)}")

    # Region ports
    region_ws_ports = []
    for i in range(width):
        output.append(f"REGION_{i}_PORT_TCP={TCP(region_port)}")
        output.append(f"REGION_{i}_PORT_HTTP={HTTP(region_port)}")
        output.append(f"REGION_{i}_PORT_WS={WS(region_port)}")
        region_ws_ports.append(WS(region_port))
        region_port += 3
        
    # Zone ports
    for i in range(width):
        for j in range(width):
            output.append(f"ZONE_{i}_{j}_PORT_TCP={TCP(zone_port)}")
            output.append(f"ZONE_{i}_{j}_PORT_HTTP={HTTP(zone_port)}")
            output.append(f"ZONE_{i}_{j}_PORT_WS={WS(zone_port)}")
            zone_port += 3
    output.append("")

    # Dom URLs
    output.append("# DOM Websocket URLs")
    for i in range(width):
        output.append(f"REGION_{i}_DOM_URL=ws://127.0.0.1")
        for j in range(width):
            output.append(f"ZONE_{i}_{j}_DOM_URL=ws://127.0.0.1")
    output.append("")

    # Sub URLs
    output.append("# SUB Websocket URLs")
    prime_sub_urls = ",".join([f"ws://127.0.0.1:{port}" for port in region_ws_ports])
    output.append(f"PRIME_SUB_URLS={prime_sub_urls}")
    output.append("")

    for i in range(width):
        zone_ws_ports = [port + 2 for port in range(data["initial_zone_port"] + i * width * 3, data["initial_zone_port"] + (i+1) * width * 3, 3)]
        region_sub_urls = ",".join([f"ws://127.0.0.1:{port}" for port in zone_ws_ports])
        output.append(f"REGION_{i}_SUB_URLS={region_sub_urls}")
    output.append("")

    # Slices
    output.append("# Slices that are running")
    slices = []
    for i in range(width):
        for j in range(width):
            slices.append(f"[{i} {j}]")
    output.append(f"SLICES=\"{','.join(slices)}\"")
    output.append(f"WIDTH={width}")
    output.append("")

    # Variables
    variables = [
        f"ENABLE_HTTP={data['ENABLE_HTTP']}",
        f"ENABLE_WS={data['ENABLE_WS']}",
        f"ENABLE_UNLOCK={data['ENABLE_UNLOCK']}",
        f"ENABLE_ARCHIVE={data['ENABLE_ARCHIVE']}",
        f"NETWORK={data['NETWORK']}",
        f"NONCE={data['NONCE']}",
        f"HTTP_ADDR={data['HTTP_ADDR']}",
        f"WS_ADDR={data['WS_ADDR']}",
        f"WS_API={','.join(data['WS_API'])}",
        f"HTTP_API={data['HTTP_API']}",
        f"QUAI_MINING={data['QUAI_MINING']}",
        f"HTTP_CORSDOMAIN=\"{data['HTTP_CORSDOMAIN']}\"",
        f"WS_ORIG=\"{data['WS_ORIG']}\"",
        f"BOOTNODE={data['BOOTNODE']}",
        f"CORS={data['CORS']}",
        f"VERBOSITY={data['VERBOSITY']}",
        f"SYNCMODE={data['SYNCMODE']}",
        f"NO_DISCOVER={data['NO_DISCOVER']}",
        f"QUAI_STATS={data['QUAI_STATS']}",
        f"STATS_NAME={data['STATS_NAME']}",
        f"STATS_PASS={data['STATS_PASS']}",
        f"STATS_HOST={data['STATS_HOST']}",
        f"SHOW_COLORS={data['SHOW_COLORS']}",
        f"RUN_BLAKE3={data['RUN_BLAKE3']}",
        f"DB_ENGINE={data['DB_ENGINE']}",
        f"ENABLE_NAT={data['ENABLE_NAT']}"
        # EXT_IP is optional, enable it if using port forwarding
        # f"EXT_IP={data['EXT_IP']}"
    ]
    output.append("\n".join(variables))

    print("** Note ** Please add each of your mining rewards addresses manually to the generated_network.env file")
    print(" * Unless they have been configured in this python script (recommended)")
    print("ZONE_0_0_COINBASE=0x0000000000000000000000000000000000000000")
    print("ZONE_X_Y_COINBASE=0x0000000000000000000000000000000000000000")

    with open('generated_network.env', 'w') as f:
        f.write("\n".join(output))
"""

def generate_network_env(input_json):
    with open(input_json, 'r') as f:
        data = json.load(f)
        
        prime_port = data["initial_prime_port"]
        region_port = data["initial_region_port"]
        zone_port = data["initial_zone_port"]
        width = data["width"]

    output = []
    output.append("include network_configs/ports.env")
    output.append("include network_configs/urls.env")
    output.append("include network_configs/addresses.env")
    output.append("")

    # Check and create network_configs directory if not exists
    if not os.path.exists("network_configs"):
        os.makedirs("network_configs")

    # Ports
    # --------------------------------------------------
    ports_output = []

    # Prime
    ports_output.append("# Ports (TCP/UCP), HTTP, WS")
    ports_output.append(f"PRIME_PORT_TCP={TCP(prime_port)}")
    ports_output.append(f"PRIME_PORT_HTTP={HTTP(prime_port)}")
    ports_output.append(f"PRIME_PORT_WS={WS(prime_port)}")

    # Region
    region_ws_ports = []
    for i in range(width):
        ports_output.append(f"REGION_{i}_PORT_TCP={TCP(region_port)}")
        ports_output.append(f"REGION_{i}_PORT_HTTP={HTTP(region_port)}")
        ports_output.append(f"REGION_{i}_PORT_WS={WS(region_port)}")
        region_ws_ports.append(WS(region_port))
        region_port += 3

    # Zone
    for i in range(width):
        for j in range(width):
            ports_output.append(f"ZONE_{i}_{j}_PORT_TCP={TCP(zone_port)}")
            ports_output.append(f"ZONE_{i}_{j}_PORT_HTTP={HTTP(zone_port)}")
            ports_output.append(f"ZONE_{i}_{j}_PORT_WS={WS(zone_port)}")
            zone_port += 3

    with open('network_configs/ports.env', 'w') as f:
        f.write("\n".join(ports_output))
    # --------------------------------------------------

    # Dom and Sub URLs
    # --------------------------------------------------
    urls_output = []
    urls_output.append("# DOM Websocket URLs")
    for i in range(width):
        urls_output.append(f"REGION_{i}_DOM_URL=ws://127.0.0.1")
        for j in range(width):
            urls_output.append(f"ZONE_{i}_{j}_DOM_URL=ws://127.0.0.1")
    urls_output.append("")

    urls_output.append("# SUB Websocket URLs")
    prime_sub_urls = ",".join([f"ws://127.0.0.1:{port}" for port in region_ws_ports])
    urls_output.append(f"PRIME_SUB_URLS={prime_sub_urls}")
    urls_output.append("")

    for i in range(width):
        zone_ws_ports = [port + 2 for port in range(data["initial_zone_port"] + i * width * 3, data["initial_zone_port"] + (i+1) * width * 3, 3)]
        region_sub_urls = ",".join([f"ws://127.0.0.1:{port}" for port in zone_ws_ports])
        urls_output.append(f"REGION_{i}_SUB_URLS={region_sub_urls}")

    with open('network_configs/urls.env', 'w') as f:
        f.write("\n".join(urls_output))
    # --------------------------------------------------

    # Coinbase Addresses
    # --------------------------------------------------
    addresses_output = []
    coinbase_addresses = []
    for i in range(width):
        for j in range(width):
            coinbase_addresses.append(f"ZONE_{i}_{j}_COINBASE={coinbase_address(i, j)}")
    addresses_output.append("# Unique Coinbase addresses")
    addresses_output.extend(coinbase_addresses)

    with open('network_configs/addresses.env', 'w') as f:
        f.write("\n".join(addresses_output))

    # --------------------------------------------------

    # Slices
    output.append("# Slices that are running")
    slices = []
    for i in range(width):
        for j in range(width):
            slices.append(f"[{i} {j}]")
    output.append(f"SLICES=\"{','.join(slices)}\"")
    output.append(f"WIDTH={width}")
    output.append("")

    # Variables
    variables = [
        f"ENABLE_HTTP={data['ENABLE_HTTP']}",
        f"ENABLE_WS={data['ENABLE_WS']}",
        f"ENABLE_UNLOCK={data['ENABLE_UNLOCK']}",
        f"ENABLE_ARCHIVE={data['ENABLE_ARCHIVE']}",
        f"NETWORK={data['NETWORK']}",
        f"NONCE={data['NONCE']}",
        f"HTTP_ADDR={data['HTTP_ADDR']}",
        f"WS_ADDR={data['WS_ADDR']}",
        f"WS_API={','.join(data['WS_API'])}",
        f"HTTP_API={data['HTTP_API']}",
        f"QUAI_MINING={data['QUAI_MINING']}",
        f"HTTP_CORSDOMAIN=\"{data['HTTP_CORSDOMAIN']}\"",
        f"WS_ORIG=\"{data['WS_ORIG']}\"",
        f"BOOTNODE={data['BOOTNODE']}",
        f"CORS={data['CORS']}",
        f"VERBOSITY={data['VERBOSITY']}",
        f"SYNCMODE={data['SYNCMODE']}",
        f"NO_DISCOVER={data['NO_DISCOVER']}",
        f"QUAI_STATS={data['QUAI_STATS']}",
        f"STATS_NAME={data['STATS_NAME']}",
        f"STATS_PASS={data['STATS_PASS']}",
        f"STATS_HOST={data['STATS_HOST']}",
        f"SHOW_COLORS={data['SHOW_COLORS']}",
        f"RUN_BLAKE3={data['RUN_BLAKE3']}",
        f"DB_ENGINE={data['DB_ENGINE']}",
        f"ENABLE_NAT={data['ENABLE_NAT']}"
        # EXT_IP is optional, enable it if using port forwarding
        # f"EXT_IP={data['EXT_IP']}"
    ]
    output.append("\n".join(variables))

    print("** Note ** Please add each of your mining rewards addresses manually to the generated_network.env file")
    print(" * Unless they have been configured in this python script (recommended)")
    print("ZONE_0_0_COINBASE=0x0000000000000000000000000000000000000000")
    print("ZONE_X_Y_COINBASE=0x0000000000000000000000000000000000000000")

    with open('network.env', 'w') as f:
        f.write("\n".join(output))

def generate_values_yaml(input_json):
    if not os.path.exists("helm"):
        os.makedirs("helm")

    with open(input_json, 'r') as f:
        data = json.load(f)

        prime_port = data["initial_prime_port"]
        region_port = data["initial_region_port"]
        zone_port = data["initial_zone_port"]
        width = data["width"]
        region_names = data["region_names"]

    output = []

    # goQuai values
    output.append("goQuai:")
    output.append("  image:")
    output.append(f"    name: {data['image_name']}")
    output.append(f"    version: {data['image_version']}")
    output.append(f"  env: {data['go_quai_env']}")
    output.append(f"  replicas: {data['replicas']}")
    output.append(f"  network: {data['network']}")
    slices = []
    for i in range(width):
        for j in range(width):
            slices.append(f"[{i} {j}]")
    output.append(f"  slices: '{','.join(slices)}'")
    output.append(f"  verbosity: {data['VERBOSITY']}")
    output.append("  chains:")

    # Prime chain
    output.append("    - name: prime")
    output.append("      ports:")
    output.append(f"        http: \"{HTTP(prime_port)}\"")
    output.append(f"        ws: \"{WS(prime_port)}\"")
    output.append(f"        disc: \"{TCP(prime_port)}\"")
    output.append("      region: \"\"")
    output.append("      zone: \"\"")
    output.append("      sub: " + ",".join([f"ws://127.0.0.1:{WS(region_port) + i*3}" for i in range(width)]))
    output.append("      dom: \"\"")

    # Region chains
    for i in range(width):
        region_name = region_names[i]
        output.append(f"    - name: {region_name}")
        output.append("      ports:")
        output.append(f"        http: \"{HTTP(region_port)}\"")
        output.append(f"        ws: \"{WS(region_port)}\"")
        output.append(f"        disc: \"{TCP(region_port)}\"")
        output.append(f"      region: {i}")
        output.append("      zone: \"\"")
        output.append("      sub: " + ",".join([f"ws://127.0.0.1:{WS(zone_port) + j*3}" for j in range(width)]))
        output.append(f"      dom: ws://127.0.0.1:{WS(prime_port)}")
        region_port += 3        

    # Zone chains
    for i in range(width):
        for j in range(width):
            region_name = region_names[i]
            output.append(f"    - name: {region_name}{j + 1}")
            output.append("      ports:")
            output.append(f"        http: \"{HTTP(zone_port)}\"")
            output.append(f"        ws: \"{WS(zone_port)}\"")
            output.append(f"        disc: \"{TCP(zone_port)}\"")
            output.append(f"      region: {i}")
            output.append(f"      zone: {j}")
            output.append("      sub: \"\"")
            output.append(f"      dom: ws://127.0.0.1:{WS(region_port) + i*3 - 3}")
            zone_port += 3


    # Write to values.yaml
    with open('helm/values.yaml', 'w') as f:
        f.write("\n".join(output))

def generate_dockerfile(input_json):
    with open(input_json, 'r') as f:
        data = json.load(f)

        prime_port = data["initial_prime_port"]
        region_port = data["initial_region_port"]
        zone_port = data["initial_zone_port"]
        width = data["width"]

    output = []

    # Set labels for the final image
    output.append("# Support setting various labels on the final image")
    output.append("ARG COMMIT=\"\"")
    output.append("ARG VERSION=\"\"")
    output.append("ARG BUILDNUM=\"\"")

    # Build Quai in a stock Go builder container
    output.append("\n# Build Quai in a stock Go builder container")
    output.append("FROM golang:1.20-alpine as builder")
    output.append("RUN apk add --no-cache gcc musl-dev linux-headers git")
    output.append("ADD . /go-quai")
    output.append("WORKDIR /go-quai")
    output.append("RUN env GO111MODULE=on go run build/ci.go install ./cmd/go-quai")

    # Stage 2
    output.append("\n# Stage 2")
    output.append("FROM golang:1.20-alpine")

    # Expose ports
    # Prime ports
    output.append("\n# Prime")
    output.append(f"EXPOSE {HTTP(prime_port)} {WS(prime_port)} {TCP(prime_port)} {TCP(prime_port)}/udp")

    # Region ports
    output.append("\n# Region")
    for _ in range(width):
        output.append(f"EXPOSE {HTTP(region_port)} {WS(region_port)} {TCP(region_port)} {TCP(region_port)}/udp")
        region_port += 3

    # Zone ports
    output.append("\n# Zone")
    for _ in range(width**2):  # width^2 zones
        output.append(f"EXPOSE {HTTP(zone_port)} {WS(zone_port)} {TCP(zone_port)} {TCP(zone_port)}/udp")
        zone_port += 3

    # Copy binaries and other necessary files
    output.append("\nCOPY --from=builder /go-quai/build/bin ./build/bin")
    output.append("COPY --from=builder /go-quai/VERSION ./VERSION")
    output.append("COPY --from=builder /go-quai/genallocs ./genallocs")
    output.append("WORKDIR ./")

    # Write to Dockerfile
    with open('Dockerfile', 'w') as f:
        f.write("\n".join(output))

def generate_docker_compose(input_json):
    with open(input_json, 'r') as f:
        data = json.load(f)

        prime_port = data["initial_prime_port"]
        region_port = data["initial_region_port"]
        zone_port = data["initial_zone_port"]
        width = data["width"]
        regions = data["region_names"]

    services = []

    # Prime Service
    services.append("services:")
    services.append("  prime:")
    services.append("    env_file:")
    services.append("      - network.env")
    services.append("    environment:")
    services.append(f"      - TCP_PORT={TCP(prime_port)}")
    services.append(f"      - HTTP_PORT={HTTP(prime_port)}")
    services.append(f"      - WS_PORT={WS(prime_port)}")
    services.append("    build: .")
    sub_urls = [f"ws://{regions[i]}:{WS(region_port) + 3*i}" for i in range(width)]
    prime_command = f'''sh -c './build/bin/go-quai --$$NETWORK --slices "$$SLICES" --syncmode full --http --http.vhosts=* --ws --http.addr 0.0.0.0 --http.api eth,net,web3,quai,txpool,debug --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai,txpool,debug --port $$TCP_PORT --http.port $$HTTP_PORT --ws.port $$WS_PORT --ws.origins=* --http.corsdomain=* --gcmode archive --nonce $$NONCE --sub.urls {','.join(sub_urls)}' '''
    services.append(f"    command: {prime_command}")
    services.append("    ports:")
    services.append(f"      - \"{TCP(prime_port)}\"")
    services.append(f"      - \"{HTTP(prime_port)}\"")
    services.append(f"      - \"{WS(prime_port)}\"")
    services.append("    volumes:")
    services.append("      - ~/.quai:/root/.quai")

    old_region_port = region_port
    # Generate for Regions
    for i in range(width):
        region = regions[i]
        services.append(f"\n  {region}:")
        services.append("    env_file:")
        services.append("      - network.env")
        services.append("    environment:")
        services.append(f"      - TCP_PORT={TCP(region_port)}")
        services.append(f"      - HTTP_PORT={HTTP(region_port)}")
        services.append(f"      - WS_PORT={WS(region_port)}")
        services.append("    build: .")
        zone_urls = [f"ws://{region}{z + 1}:{WS(zone_port) + 3*z}" for z in range(width)]
        dom_url = f"ws://prime:{TCP(prime_port)}"
        region_command = f'''sh -c './build/bin/go-quai --$$NETWORK --slices "$$SLICES" --syncmode full --http --http.vhosts=* --ws --http.addr 0.0.0.0 --http.api eth,net,web3,quai,txpool,debug --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai,txpool,debug --port $$TCP_PORT --http.port $$HTTP_PORT --ws.port $$WS_PORT --ws.origins=* --http.corsdomain=* --gcmode archive --nonce $$NONCE --region {i} --sub.urls {','.join(zone_urls)} --dom.url {dom_url}' '''
        services.append(f"    command: {region_command}")
        services.append("    ports:")
        services.append(f"      - \"{TCP(region_port)}\"")
        services.append(f"      - \"{HTTP(region_port)}\"")
        services.append(f"      - \"{WS(region_port)}\"")
        services.append("    volumes:")
        services.append("      - ~/.quai:/root/.quai")

        region_port += 3

    region_port = old_region_port
    # Generate for Zones
    for i in range(width):
        region = regions[i]
        dom_url = f"ws://{region}:{WS(region_port)}"
        # Zones for each region
        for j in range(width):
            zone = region + str(j+1)
            services.append(f"\n  {zone}:")
            services.append("    env_file:")
            services.append("      - network.env")
            services.append("    environment:")
            services.append(f"      - TCP_PORT={TCP(zone_port)}")
            services.append(f"      - HTTP_PORT={HTTP(zone_port)}")
            services.append(f"      - WS_PORT={WS(zone_port)}")
            services.append(f"      - COINBASE_ADDR={coinbase_address(i, j)}")
            services.append("    build: .")
            zone_command = f'''sh -c './build/bin/go-quai --$$NETWORK --slices "$$SLICES" --syncmode full --http --http.vhosts=* --ws --miner.etherbase $$COINBASE_ADDR --http.addr 0.0.0.0 --http.api eth,net,web3,quai,txpool,debug --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai,txpool,debug --port $$TCP_PORT --http.port $$HTTP_PORT --ws.port $$WS_PORT --ws.origins=* --http.corsdomain=* --gcmode archive --nonce $$NONCE --region {i} --zone {j} --dom.url {dom_url}' '''
            services.append(f"    command: {zone_command}")
            services.append("    ports:")
            services.append(f"      - \"{TCP(zone_port)}\"")
            services.append(f"      - \"{HTTP(zone_port)}\"")
            services.append(f"      - \"{WS(zone_port)}\"")
            services.append("    volumes:")
            services.append("      - ~/.quai:/root/.quai")
            
            zone_port += 3
        region_port += 3

    # Write to docker-compose.yml
    with open('docker-compose.yml', 'w') as f:
        f.write("\n".join(services))


generate_docker_compose('setup_config.json')
generate_dockerfile('setup_config.json')
generate_values_yaml('setup_config.json')
generate_network_env('setup_config.json')

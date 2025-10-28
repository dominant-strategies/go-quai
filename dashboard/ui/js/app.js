// Quai Mining Pool Dashboard

class Dashboard {
    constructor() {
        this.ws = null;
        this.globe = null;
        this.hashrateChart = null;
        this.hashrateHistory = [];
        this.maxHistoryPoints = 60;
        this.workers = [];
        this.trackedAddresses = this.loadTrackedAddresses();

        this.init();
    }

    async init() {
        this.initTabs();
        this.initGlobe();
        this.initChart();
        this.connectWebSocket();
        await this.fetchInitialData();
        this.renderTrackedAddresses();

        // Fallback polling
        setInterval(() => this.pollData(), 5000);
    }

    // Tab Navigation
    initTabs() {
        const tabs = document.querySelectorAll('.nav-tab');
        tabs.forEach(tab => {
            tab.addEventListener('click', () => {
                const tabId = tab.dataset.tab;
                this.switchTab(tabId);
            });
        });
    }

    switchTab(tabId) {
        // Update nav tabs
        document.querySelectorAll('.nav-tab').forEach(t => t.classList.remove('active'));
        document.querySelector(`.nav-tab[data-tab="${tabId}"]`).classList.add('active');

        // Update content
        document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
        document.getElementById(`${tabId}-tab`).classList.add('active');

        // Resize globe if switching to pool tab
        if (tabId === 'pool' && this.globe) {
            const container = document.getElementById('globe-container');
            if (container) {
                this.globe.width(container.clientWidth);
            }
        }
    }

    // Globe Visualization
    initGlobe() {
        const container = document.getElementById('globe-container');
        if (!container || typeof Globe === 'undefined') {
            console.warn('Globe.gl not loaded or container not found');
            return;
        }

        this.globe = Globe()
            .globeImageUrl('https://unpkg.com/three-globe/example/img/earth-dark.jpg')
            .backgroundColor('rgba(0,0,0,0)')
            .showAtmosphere(true)
            .atmosphereColor('#f7931a')
            .atmosphereAltitude(0.1)
            .pointsData([])
            .pointLat(d => d.latitude)
            .pointLng(d => d.longitude)
            .pointColor(() => '#f7931a')
            .pointAltitude(0.01)
            .pointRadius(0.4)
            .pointsMerge(true)
            .width(container.clientWidth)
            .height(280)
            (container);

        this.globe.controls().autoRotate = true;
        this.globe.controls().autoRotateSpeed = 0.3;
        this.globe.controls().enableZoom = false;

        window.addEventListener('resize', () => {
            if (this.globe && container) {
                this.globe.width(container.clientWidth);
            }
        });
    }

    updateGlobe(peers) {
        if (!this.globe) return;
        const points = peers.filter(p => p.latitude && p.longitude);
        this.globe.pointsData(points);
        document.getElementById('globe-peer-count').textContent = peers.length;
    }

    // Hashrate Chart
    initChart() {
        const canvas = document.getElementById('hashrate-chart');
        if (!canvas || typeof Chart === 'undefined') {
            console.warn('Chart.js not loaded or canvas not found');
            return;
        }

        const ctx = canvas.getContext('2d');
        const containerHeight = canvas.parentElement?.clientHeight || 280;
        const gradient = ctx.createLinearGradient(0, 0, 0, containerHeight);
        gradient.addColorStop(0, 'rgba(247, 147, 26, 0.3)');
        gradient.addColorStop(1, 'rgba(247, 147, 26, 0)');

        this.hashrateChart = new Chart(ctx, {
            type: 'line',
            data: {
                labels: [],
                datasets: [{
                    label: 'Hashrate',
                    data: [],
                    borderColor: '#f7931a',
                    backgroundColor: gradient,
                    fill: true,
                    tension: 0.4,
                    pointRadius: 0,
                    borderWidth: 2,
                    pointHoverRadius: 4,
                    pointHoverBackgroundColor: '#f7931a',
                    pointHoverBorderColor: '#fff',
                    pointHoverBorderWidth: 2
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                plugins: {
                    legend: { display: false },
                    tooltip: {
                        mode: 'index',
                        intersect: false,
                        backgroundColor: 'rgba(18, 18, 26, 0.95)',
                        titleColor: '#fff',
                        bodyColor: '#9ca3af',
                        borderColor: 'rgba(255, 255, 255, 0.1)',
                        borderWidth: 1,
                        padding: 12,
                        displayColors: false,
                        callbacks: {
                            label: (context) => this.formatHashrate(context.raw)
                        }
                    }
                },
                scales: {
                    x: {
                        display: false,
                        grid: { display: false }
                    },
                    y: {
                        beginAtZero: true,
                        border: { display: false },
                        grid: { color: 'rgba(255, 255, 255, 0.03)' },
                        ticks: {
                            color: '#6b7280',
                            font: { family: "'JetBrains Mono', monospace", size: 10 },
                            callback: (value) => this.formatHashrate(value)
                        }
                    }
                },
                interaction: { intersect: false, mode: 'index' }
            }
        });
    }

    updateChart(hashrate) {
        if (!this.hashrateChart) return;

        const now = new Date().toLocaleTimeString();
        this.hashrateHistory.push({ time: now, value: hashrate });

        if (this.hashrateHistory.length > this.maxHistoryPoints) {
            this.hashrateHistory.shift();
        }

        this.hashrateChart.data.labels = this.hashrateHistory.map(h => h.time);
        this.hashrateChart.data.datasets[0].data = this.hashrateHistory.map(h => h.value);
        this.hashrateChart.update('none');
    }

    // WebSocket Connection
    connectWebSocket() {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/ws`;

        try {
            this.ws = new WebSocket(wsUrl);

            this.ws.onopen = () => console.log('WebSocket connected');

            this.ws.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data);
                    this.handleUpdate(data);
                } catch (e) {
                    console.error('Failed to parse WebSocket message:', e);
                }
            };

            this.ws.onclose = () => {
                console.log('WebSocket disconnected, reconnecting...');
                setTimeout(() => this.connectWebSocket(), 3000);
            };

            this.ws.onerror = (error) => console.error('WebSocket error:', error);
        } catch (e) {
            console.error('WebSocket connection failed:', e);
        }
    }

    handleUpdate(data) {
        if (data.stratum) {
            this.updatePoolStats(data.stratum.overview);
            if (data.stratum.workers) {
                this.workers = data.stratum.workers;
                this.updateWorkers(data.stratum.workers);
                this.updateTrackedAddressesData();
            }
        }

        if (data.peerCount !== undefined) {
            document.getElementById('peer-count').textContent = data.peerCount;
        }

        if (data.sync) {
            this.updateSyncStatus(data.sync);
        }

        if (data.nodeInfo) {
            this.updateNodeInfo(data.nodeInfo);
        }

        if (data.urls) {
            this.updateUrls(data.urls);
        }
    }

    // Data Fetching
    async fetchInitialData() {
        try {
            const [overview, workers, blocks, peers] = await Promise.all([
                this.fetchJSON('/api/overview'),
                this.fetchJSON('/api/workers'),
                this.fetchJSON('/api/blocks'),
                this.fetchJSON('/api/peers')
            ]);

            if (overview.stratum) {
                this.updatePoolStats(overview.stratum);
            }
            if (overview.peerCount !== undefined) {
                document.getElementById('peer-count').textContent = overview.peerCount;
            }
            if (overview.sync) {
                this.updateSyncStatus(overview.sync);
            }
            if (overview.nodeInfo) {
                this.updateNodeInfo(overview.nodeInfo);
            }
            if (overview.urls) {
                this.updateUrls(overview.urls);
            }

            this.workers = workers || [];
            this.updateWorkers(workers);
            this.updateBlocks(blocks);
            this.updateGlobe(peers || []);
            this.updateTrackedAddressesData();
        } catch (e) {
            console.error('Failed to fetch initial data:', e);
        }
    }

    async pollData() {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) return;

        try {
            const overview = await this.fetchJSON('/api/overview');
            if (overview.stratum) {
                this.updatePoolStats(overview.stratum);
            }
        } catch (e) {
            // Silent fail for polling
        }
    }

    async fetchJSON(url) {
        const response = await fetch(url);
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        return response.json();
    }

    // UI Updates
    updatePoolStats(stats) {
        if (!stats) return;

        const hashrate = stats.hashrate || 0;
        document.getElementById('pool-hashrate').textContent = this.formatHashrate(hashrate);
        document.getElementById('pool-workers').textContent = stats.workersConnected || 0;
        document.getElementById('pool-blocks').textContent = stats.blocksFound || 0;

        // Calculate efficiency
        const total = (stats.sharesValid || 0) + (stats.sharesStale || 0) + (stats.sharesInvalid || 0);
        const efficiency = total > 0 ? ((stats.sharesValid || 0) / total * 100) : 100;
        document.getElementById('pool-efficiency').textContent = efficiency.toFixed(1) + '%';

        this.updateChart(hashrate);
    }

    updateWorkers(workers) {
        const tbody = document.getElementById('workers-tbody');
        if (!tbody) return;

        if (!workers || workers.length === 0) {
            tbody.innerHTML = '<tr class="empty-row"><td colspan="7">No workers connected</td></tr>';
            return;
        }

        tbody.innerHTML = workers.map(w => `
            <tr>
                <td class="address">${this.truncateAddress(w.address)}</td>
                <td>${w.workerName || 'default'}</td>
                <td><span class="algo-badge">${(w.algorithm || 'sha').toUpperCase()}</span></td>
                <td>${this.formatHashrate(w.hashrate || 0)}</td>
                <td>${this.formatNumber(w.sharesValid || 0)}</td>
                <td>${w.sharesInvalid || 0}</td>
                <td>${this.timeAgo(w.lastShareAt)}</td>
            </tr>
        `).join('');
    }

    updateBlocks(blocks) {
        const tbody = document.getElementById('blocks-tbody');
        if (!tbody) return;

        if (!blocks || blocks.length === 0) {
            tbody.innerHTML = '<tr class="empty-row"><td colspan="5">No blocks found yet</td></tr>';
            return;
        }

        tbody.innerHTML = blocks.map(b => `
            <tr>
                <td><strong>#${this.formatNumber(b.height)}</strong></td>
                <td class="hash">${this.truncateHash(b.hash)}</td>
                <td class="address">${this.truncateAddress(b.coinbase)}</td>
                <td>${b.txCount || 0}</td>
                <td>${this.timeAgo(b.timestamp)}</td>
            </tr>
        `).join('');
    }

    updateSyncStatus(sync) {
        if (!sync) return;

        const navStatusDot = document.getElementById('nav-status-dot');
        const navSyncText = document.getElementById('nav-sync-text');
        const syncIcon = document.getElementById('sync-icon');
        const syncTitle = document.getElementById('sync-title');
        const syncSubtitle = document.getElementById('sync-subtitle');
        const progressContainer = document.getElementById('sync-progress-container');
        const progressFill = document.getElementById('sync-progress-fill');
        const progressText = document.getElementById('sync-progress-text');

        if (sync.isSyncing) {
            const progress = (sync.syncProgress * 100).toFixed(1);

            navStatusDot.className = 'status-dot syncing';
            navSyncText.textContent = `SYNCING ${progress}%`;

            syncIcon.textContent = '↻';
            syncIcon.className = 'sync-icon syncing';
            syncTitle.textContent = 'Syncing...';
            syncSubtitle.textContent = `Block ${sync.currentBlock} of ${sync.highestBlock}`;

            progressContainer.style.display = 'block';
            progressFill.style.width = progress + '%';
            progressText.textContent = progress + '%';
        } else {
            navStatusDot.className = 'status-dot synced';
            navSyncText.textContent = 'SYNCED';

            syncIcon.textContent = '✓';
            syncIcon.className = 'sync-icon';
            syncTitle.textContent = 'Fully Synced';
            syncSubtitle.textContent = 'Node is up to date with the network';

            progressContainer.style.display = 'none';
        }
    }

    updateNodeInfo(info) {
        if (!info) return;

        if (info.version) {
            document.getElementById('node-version').textContent = info.version;
            document.getElementById('footer-version').textContent = 'v' + info.version;
        }
        if (info.network) {
            document.getElementById('node-network').textContent = info.network;
        }
        if (info.location) {
            document.getElementById('node-location').textContent = info.location;
        }
        if (info.blockHeight !== undefined) {
            document.getElementById('block-height').textContent = this.formatNumber(info.blockHeight);
        }
        if (info.chainId) {
            document.getElementById('chain-id').textContent = info.chainId;
        }
        if (info.uptime) {
            document.getElementById('node-uptime').textContent = this.formatUptime(info.uptime);
        }
    }

    updateUrls(urls) {
        if (!urls) return;

        if (urls.rpc) {
            const rpcEl = document.querySelector('#rpc-url code');
            if (rpcEl) rpcEl.textContent = urls.rpc;
        }
        if (urls.ws) {
            const wsEl = document.querySelector('#ws-url code');
            if (wsEl) wsEl.textContent = urls.ws;
        }

        // Update stratum URLs (multi-port support)
        if (urls.stratumSha) {
            const el = document.querySelector('#stratum-sha-url code');
            if (el) el.textContent = urls.stratumSha;
            document.getElementById('stratum-sha-card')?.classList.remove('hidden');
        } else {
            document.getElementById('stratum-sha-card')?.classList.add('hidden');
        }

        if (urls.stratumScrypt) {
            const el = document.querySelector('#stratum-scrypt-url code');
            if (el) el.textContent = urls.stratumScrypt;
            document.getElementById('stratum-scrypt-card')?.classList.remove('hidden');
        } else {
            document.getElementById('stratum-scrypt-card')?.classList.add('hidden');
        }

        if (urls.stratumKawpow) {
            const el = document.querySelector('#stratum-kawpow-url code');
            if (el) el.textContent = urls.stratumKawpow;
            document.getElementById('stratum-kawpow-card')?.classList.remove('hidden');

            // Update guide with kawpow host
            const host = urls.stratumKawpow.replace('stratum+tcp://', '');
            const guideHost = document.getElementById('guide-stratum-host');
            if (guideHost) guideHost.textContent = host;
            const guidePort = document.getElementById('guide-kawpow-port');
            if (guidePort) guidePort.textContent = host.split(':')[1] || '3333';
        } else {
            document.getElementById('stratum-kawpow-card')?.classList.add('hidden');
        }

        // Update SHA guide
        if (urls.stratumSha) {
            const host = urls.stratumSha.replace('stratum+tcp://', '');
            const guideHost = document.getElementById('guide-sha-host');
            if (guideHost) guideHost.textContent = host;
            const guidePort = document.getElementById('guide-sha-port');
            if (guidePort) guidePort.textContent = host.split(':')[1] || '3334';
        }

        // Update Scrypt guide
        if (urls.stratumScrypt) {
            const host = urls.stratumScrypt.replace('stratum+tcp://', '');
            const guideHost = document.getElementById('guide-scrypt-host');
            if (guideHost) guideHost.textContent = host;
            const guidePort = document.getElementById('guide-scrypt-port');
            if (guidePort) guidePort.textContent = host.split(':')[1] || '3335';
        }
    }

    // Profile / Address Tracking
    loadTrackedAddresses() {
        try {
            const saved = localStorage.getItem('trackedAddresses');
            return saved ? JSON.parse(saved) : [];
        } catch (e) {
            return [];
        }
    }

    saveTrackedAddresses() {
        localStorage.setItem('trackedAddresses', JSON.stringify(this.trackedAddresses));
    }

    addTrackedAddress(address) {
        address = address.trim();
        if (!address) return;

        // Basic validation
        if (!address.startsWith('0x') || address.length < 10) {
            alert('Please enter a valid Quai address starting with 0x');
            return;
        }

        // Check for duplicates
        if (this.trackedAddresses.includes(address.toLowerCase())) {
            alert('This address is already being tracked');
            return;
        }

        this.trackedAddresses.push(address.toLowerCase());
        this.saveTrackedAddresses();
        this.renderTrackedAddresses();
        this.updateTrackedAddressesData();

        // Clear input
        document.getElementById('address-input').value = '';
    }

    removeTrackedAddress(address) {
        this.trackedAddresses = this.trackedAddresses.filter(a => a !== address.toLowerCase());
        this.saveTrackedAddresses();
        this.renderTrackedAddresses();
    }

    renderTrackedAddresses() {
        const container = document.getElementById('tracked-addresses-container');
        if (!container) return;

        if (this.trackedAddresses.length === 0) {
            container.innerHTML = `
                <div class="empty-state" id="no-addresses-msg">
                    <div class="empty-icon">👤</div>
                    <div class="empty-title">No addresses tracked</div>
                    <div class="empty-subtitle">Add your mining address above to track your workers and earnings</div>
                </div>
            `;
            return;
        }

        container.innerHTML = this.trackedAddresses.map(address => `
            <div class="tracked-address-card" data-address="${address}">
                <div class="tracked-address-header">
                    <div class="tracked-address-info">
                        <div class="tracked-address-icon">⛏</div>
                        <div>
                            <div class="tracked-address-text">${this.truncateAddress(address, 12, 8)}</div>
                            <div class="tracked-address-label">Mining Address</div>
                        </div>
                    </div>
                    <div class="tracked-address-actions">
                        <button class="btn-icon" onclick="removeAddress('${address}')" title="Remove">✕</button>
                    </div>
                </div>
                <div class="tracked-address-stats">
                    <div class="tracked-stat">
                        <div class="tracked-stat-value" id="tracked-hashrate-${address.slice(2, 10)}">0 H/s</div>
                        <div class="tracked-stat-label">Hashrate</div>
                    </div>
                    <div class="tracked-stat">
                        <div class="tracked-stat-value" id="tracked-workers-${address.slice(2, 10)}">0</div>
                        <div class="tracked-stat-label">Workers</div>
                    </div>
                    <div class="tracked-stat">
                        <div class="tracked-stat-value" id="tracked-shares-${address.slice(2, 10)}">0</div>
                        <div class="tracked-stat-label">Shares</div>
                    </div>
                    <div class="tracked-stat">
                        <div class="tracked-stat-value" id="tracked-invalid-${address.slice(2, 10)}">0</div>
                        <div class="tracked-stat-label">Invalid</div>
                    </div>
                </div>
                <div class="tracked-workers-section">
                    <div class="tracked-workers-title">Workers</div>
                    <div class="table-container">
                        <table class="data-table">
                            <thead>
                                <tr>
                                    <th>Worker Name</th>
                                    <th>Algorithm</th>
                                    <th>Hashrate</th>
                                    <th>Shares</th>
                                    <th>Last Active</th>
                                </tr>
                            </thead>
                            <tbody id="tracked-workers-tbody-${address.slice(2, 10)}">
                                <tr class="empty-row">
                                    <td colspan="5">No workers found for this address</td>
                                </tr>
                            </tbody>
                        </table>
                    </div>
                </div>
            </div>
        `).join('');
    }

    updateTrackedAddressesData() {
        if (this.trackedAddresses.length === 0 || !this.workers) return;

        this.trackedAddresses.forEach(address => {
            const addressLower = address.toLowerCase();
            const addressWorkers = this.workers.filter(w =>
                w.address && w.address.toLowerCase() === addressLower
            );

            const shortAddr = address.slice(2, 10);

            // Calculate totals
            const totalHashrate = addressWorkers.reduce((sum, w) => sum + (w.hashrate || 0), 0);
            const totalShares = addressWorkers.reduce((sum, w) => sum + (w.sharesValid || 0), 0);
            const totalInvalid = addressWorkers.reduce((sum, w) => sum + (w.sharesInvalid || 0), 0);

            // Update stats
            const hashrateEl = document.getElementById(`tracked-hashrate-${shortAddr}`);
            const workersEl = document.getElementById(`tracked-workers-${shortAddr}`);
            const sharesEl = document.getElementById(`tracked-shares-${shortAddr}`);
            const invalidEl = document.getElementById(`tracked-invalid-${shortAddr}`);

            if (hashrateEl) hashrateEl.textContent = this.formatHashrate(totalHashrate);
            if (workersEl) workersEl.textContent = addressWorkers.length;
            if (sharesEl) sharesEl.textContent = this.formatNumber(totalShares);
            if (invalidEl) invalidEl.textContent = totalInvalid;

            // Update workers table
            const tbody = document.getElementById(`tracked-workers-tbody-${shortAddr}`);
            if (tbody) {
                if (addressWorkers.length === 0) {
                    tbody.innerHTML = '<tr class="empty-row"><td colspan="5">No workers found for this address</td></tr>';
                } else {
                    tbody.innerHTML = addressWorkers.map(w => `
                        <tr>
                            <td>${w.workerName || 'default'}</td>
                            <td><span class="algo-badge">${(w.algorithm || 'sha').toUpperCase()}</span></td>
                            <td>${this.formatHashrate(w.hashrate || 0)}</td>
                            <td>${this.formatNumber(w.sharesValid || 0)}</td>
                            <td>${this.timeAgo(w.lastShareAt)}</td>
                        </tr>
                    `).join('');
                }
            }
        });
    }

    // Formatters
    formatHashrate(h) {
        if (h === 0) return '0 H/s';
        const units = ['H/s', 'KH/s', 'MH/s', 'GH/s', 'TH/s', 'PH/s', 'EH/s'];
        let unitIndex = 0;
        while (h >= 1000 && unitIndex < units.length - 1) {
            h /= 1000;
            unitIndex++;
        }
        return h.toFixed(2) + ' ' + units[unitIndex];
    }

    formatNumber(n) {
        if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
        if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
        return n.toString();
    }

    formatUptime(seconds) {
        if (seconds < 60) return seconds + 's';
        if (seconds < 3600) return Math.floor(seconds / 60) + 'm ' + (seconds % 60) + 's';
        if (seconds < 86400) {
            const h = Math.floor(seconds / 3600);
            const m = Math.floor((seconds % 3600) / 60);
            return h + 'h ' + m + 'm';
        }
        const d = Math.floor(seconds / 86400);
        const h = Math.floor((seconds % 86400) / 3600);
        return d + 'd ' + h + 'h';
    }

    truncateAddress(addr, prefixLen = 8, suffixLen = 6) {
        if (!addr) return 'Unknown';
        if (addr.length <= prefixLen + suffixLen + 3) return addr;
        return addr.slice(0, prefixLen) + '...' + addr.slice(-suffixLen);
    }

    truncateHash(hash) {
        if (!hash) return '';
        if (hash.length <= 20) return hash;
        return hash.slice(0, 10) + '...' + hash.slice(-8);
    }

    timeAgo(timestamp) {
        if (!timestamp) return 'Never';

        const now = new Date();
        const then = new Date(timestamp);
        const diff = Math.floor((now - then) / 1000);

        if (diff < 0) return 'Just now';
        if (diff < 60) return diff + 's ago';
        if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
        if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
        return Math.floor(diff / 86400) + 'd ago';
    }
}

// Global functions
function refreshWorkers() {
    if (window.dashboard) {
        window.dashboard.fetchJSON('/api/workers')
            .then(workers => {
                window.dashboard.workers = workers || [];
                window.dashboard.updateWorkers(workers);
                window.dashboard.updateTrackedAddressesData();
            })
            .catch(console.error);
    }
}

function copyUrl(elementId) {
    const element = document.getElementById(elementId);
    const code = element.querySelector('code');
    const text = code.textContent;

    navigator.clipboard.writeText(text).then(() => {
        const copyBtn = element.querySelector('.copy-btn');
        const originalText = copyBtn.textContent;
        copyBtn.textContent = 'Copied!';
        copyBtn.style.color = '#22c55e';

        setTimeout(() => {
            copyBtn.textContent = originalText;
            copyBtn.style.color = '';
        }, 2000);
    }).catch(err => {
        console.error('Failed to copy:', err);
    });
}

function copyCode(elementId) {
    const element = document.getElementById(elementId);
    const text = element.textContent;

    navigator.clipboard.writeText(text).then(() => {
        const btn = element.parentElement.querySelector('.copy-code-btn');
        const originalText = btn.textContent;
        btn.textContent = 'Copied!';

        setTimeout(() => {
            btn.textContent = originalText;
        }, 2000);
    }).catch(err => {
        console.error('Failed to copy:', err);
    });
}

function addTrackedAddress() {
    const input = document.getElementById('address-input');
    if (window.dashboard && input) {
        window.dashboard.addTrackedAddress(input.value);
    }
}

function removeAddress(address) {
    if (window.dashboard) {
        window.dashboard.removeTrackedAddress(address);
    }
}

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    window.dashboard = new Dashboard();
});

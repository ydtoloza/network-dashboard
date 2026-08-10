const POLL_INTERVAL = 1000;
let interfaces = [];
let charts = {};
let lastData = {};

function formatBytes(bytes, decimals = 2) {
    if (!+bytes) return '0 B';
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`;
}

function formatSpeed(bytes, prevBytes, dt) {
    if (prevBytes === undefined || dt === 0) return '0 B/s';
    const diff = bytes - prevBytes;
    if (diff < 0) return '0 B/s'; // Counter reset
    const bps = (diff / dt) * 1000;
    return formatBytes(bps) + '/s';
}

async function init() {
    // Get interfaces
    const res = await fetch('/api/interfaces');
    interfaces = await res.json();
    
    const dashboard = document.getElementById('dashboard');
    
    interfaces.forEach(iface => {
        // Build HTML for each interface
        const section = document.createElement('div');
        section.className = 'interface-section';
        section.innerHTML = `
            <h2 class="interface-header">${iface}</h2>
            <div class="stats-grid">
                <div class="stat-box">
                    <div class="stat-label">Download Speed</div>
                    <div class="stat-value rx" id="rx-speed-${iface}">0 B/s</div>
                </div>
                <div class="stat-box">
                    <div class="stat-label">Upload Speed</div>
                    <div class="stat-value tx" id="tx-speed-${iface}">0 B/s</div>
                </div>
            </div>
            <div class="charts-container">
                <div class="chart-wrapper">
                    <div class="chart-title">Real-time Traffic</div>
                    <canvas id="live-chart-${iface}"></canvas>
                </div>
                <div class="chart-wrapper">
                    <div class="chart-title">History (Last 30 Days)</div>
                    <div id="history-${iface}">Loading...</div>
                </div>
            </div>
        `;
        dashboard.appendChild(section);

        // Init Chart
        const ctx = document.getElementById(`live-chart-${iface}`).getContext('2d');
        charts[iface] = new Chart(ctx, {
            type: 'line',
            data: {
                labels: Array(60).fill(''),
                datasets: [
                    { label: 'RX', data: Array(60).fill(0), borderColor: '#4caf50', tension: 0, pointRadius: 0, borderWidth: 2 },
                    { label: 'TX', data: Array(60).fill(0), borderColor: '#2196f3', tension: 0, pointRadius: 0, borderWidth: 2 }
                ]
            },
            options: {
                responsive: true,
                animation: false,
                scales: {
                    y: { 
                        beginAtZero: true,
                        ticks: {
                            callback: function(value) {
                                return formatBytes(value);
                            }
                        }
                    },
                    x: { display: false }
                },
                plugins: {
                    legend: { display: true, position: 'top', labels: { color: '#aaa' } }
                }
            }
        });
    });

    // Start polling
    pollRealtime();
    fetchHistory();
}

async function pollRealtime() {
    try {
        const res = await fetch('/api/realtime');
        const data = await res.json();
        const now = Date.now();

        interfaces.forEach(iface => {
            if (data[iface]) {
                const current = data[iface];
                const prev = lastData[iface];
                
                let rxSpeedBytes = 0;
                let txSpeedBytes = 0;

                if (prev) {
                    const dt = current.timestamp - prev.timestamp;
                    if (dt > 0) {
                        const rxDiff = current.rx_bytes - prev.rx_bytes;
                        const txDiff = current.tx_bytes - prev.tx_bytes;
                        if (rxDiff >= 0) rxSpeedBytes = (rxDiff / dt) * 1000;
                        if (txDiff >= 0) txSpeedBytes = (txDiff / dt) * 1000;
                    }
                }

                document.getElementById(`rx-speed-${iface}`).innerText = formatBytes(rxSpeedBytes) + '/s';
                document.getElementById(`tx-speed-${iface}`).innerText = formatBytes(txSpeedBytes) + '/s';

                // Update chart
                const chart = charts[iface];
                chart.data.datasets[0].data.shift();
                chart.data.datasets[0].data.push(rxSpeedBytes);
                chart.data.datasets[1].data.shift();
                chart.data.datasets[1].data.push(txSpeedBytes);
                chart.update();

                lastData[iface] = current;
            }
        });
    } catch (e) {
        console.error("Poll error", e);
    }
    
    setTimeout(pollRealtime, POLL_INTERVAL);
}

async function fetchHistory() {
    try {
        const res = await fetch('/api/history');
        const vnstatData = await res.json();
        
        interfaces.forEach(iface => {
            const historyDiv = document.getElementById(`history-${iface}`);
            const ifaceData = vnstatData.interfaces.find(i => i.name === iface);
            
            if (!ifaceData || !ifaceData.traffic || !ifaceData.traffic.day) {
                historyDiv.innerHTML = "<p style='text-align:center; color:#888;'>No historical data yet.</p>";
                return;
            }

            const days = ifaceData.traffic.day.slice(-5).reverse(); // Last 5 days
            
            let table = `<table>
                <tr><th>Date</th><th>RX</th><th>TX</th><th>Total</th></tr>`;
            
            days.forEach(d => {
                const dateStr = `${d.date.year}-${String(d.date.month).padStart(2, '0')}-${String(d.date.day).padStart(2, '0')}`;
                table += `
                    <tr>
                        <td>${dateStr}</td>
                        <td class="rx">${formatBytes(d.rx)}</td>
                        <td class="tx">${formatBytes(d.tx)}</td>
                        <td>${formatBytes(d.rx + d.tx)}</td>
                    </tr>
                `;
            });
            table += `</table>`;
            
            historyDiv.innerHTML = table;
        });
    } catch (e) {
        console.error("History fetch error", e);
    }
    
    // Refresh history every 5 minutes
    setTimeout(fetchHistory, 5 * 60 * 1000);
}

window.onload = init;
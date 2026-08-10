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
    
    const realtimeContainer = document.getElementById('realtime-container');
    const historyContainer = document.getElementById('history-container');
    
    interfaces.forEach(iface => {
        // Build Real-time HTML
        const rtSection = document.createElement('div');
        rtSection.className = 'interface-section';
        rtSection.innerHTML = `
            <div class="interface-header-wrap">
                <span class="status-dot"></span>
                <h2 class="interface-header">${iface}</h2>
            </div>
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
            <div class="chart-wrapper">
                <canvas id="live-chart-${iface}"></canvas>
            </div>
        `;
        realtimeContainer.appendChild(rtSection);

        // Build History HTML
        const histSection = document.createElement('div');
        histSection.className = 'interface-section';
        histSection.innerHTML = `
            <h2 class="interface-header">${iface}</h2>
            <div id="history-${iface}">Loading...</div>
        `;
        historyContainer.appendChild(histSection);

        // Init Chart with better visuals
        const ctx = document.getElementById(`live-chart-${iface}`).getContext('2d');
        charts[iface] = new Chart(ctx, {
            type: 'line',
            data: {
                labels: Array(60).fill(''),
                datasets: [
                    { 
                        label: 'RX (Download)', 
                        data: Array(60).fill(0), 
                        borderColor: '#4caf50', 
                        backgroundColor: 'rgba(76, 175, 80, 0.1)',
                        fill: true,
                        tension: 0.4, 
                        pointRadius: 0, 
                        borderWidth: 2 
                    },
                    { 
                        label: 'TX (Upload)', 
                        data: Array(60).fill(0), 
                        borderColor: '#2196f3', 
                        backgroundColor: 'rgba(33, 150, 243, 0.1)',
                        fill: true,
                        tension: 0.4, 
                        pointRadius: 0, 
                        borderWidth: 2 
                    }
                ]
            },
            options: {
                responsive: true,
                animation: false,
                interaction: {
                    intersect: false,
                    mode: 'index',
                },
                scales: {
                    y: { 
                        beginAtZero: true,
                        grid: { color: '#333' },
                        ticks: {
                            callback: function(value) { return formatBytes(value); },
                            color: '#888'
                        }
                    },
                    x: { display: false }
                },
                plugins: {
                    legend: { display: true, position: 'top', labels: { color: '#e0e0e0', font: { family: 'monospace' } } },
                    tooltip: {
                        callbacks: {
                            label: function(context) { return context.dataset.label + ': ' + formatBytes(context.parsed.y) + '/s'; }
                        }
                    }
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
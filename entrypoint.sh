#!/bin/sh

# Configurar vnstat si es la primera vez
if [ ! -d "/var/lib/vnstat" ]; then
    mkdir -p /var/lib/vnstat
fi

# Agregar interfaces a vnstat basándonos en la variable de entorno
if [ -n "$INTERFACES" ]; then
    IFS=','
    for interface in $INTERFACES; do
        # Eliminar espacios en blanco
        interface=$(echo "$interface" | xargs)
        echo "Adding interface to vnstat: $interface"
        # Ignorar errores si la interfaz ya existe en la BD
        vnstat --add -i "$interface" > /dev/null 2>&1
    done
fi

# Iniciar vnstatd en segundo plano
echo "Starting vnstatd..."
vnstatd -n &
VNSTATD_PID=$!

# Reenviar señales a vnstatd para un apagado limpio
trap 'kill -TERM $VNSTATD_PID 2>/dev/null; exit 0' TERM INT

# Iniciar el servidor Go en primer plano
echo "Starting web server..."
./network-dashboard

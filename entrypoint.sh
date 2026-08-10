#!/bin/sh

# Configurar vnstat si es la primera vez
if [ ! -d "/var/lib/vnstat" ]; then
    mkdir -p /var/lib/vnstat
fi

# Agregar interfaces a vnstat basándonos en la variable de entorno
IFS=','
for interface in $INTERFACES; do
    # Eliminar espacios en blanco
    interface=$(echo "$interface" | xargs)
    echo "Adding interface to vnstat: $interface"
    # Ignorar errores si la interfaz ya existe en la BD
    vnstat --add -i "$interface" > /dev/null 2>&1
done

# Iniciar vnstatd en segundo plano
echo "Starting vnstatd..."
vnstatd -n &

# Iniciar el servidor Go en primer plano
echo "Starting web server..."
./network-dashboard

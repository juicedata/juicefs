#!/bin/sh

set -e

KERBEROS_REALM="EXAMPLE.COM"
KERBEROS_PRINCIPLE="administrator"
KERBEROS_PASSWORD="password1234"

sudo tee /etc/krb5.conf << EOF
[libdefaults]
    default_realm = $KERBEROS_REALM
    dns_lookup_realm = false
    dns_lookup_kdc = false
[realms]
    $KERBEROS_REALM = {
        kdc = localhost
        admin_server = localhost
    }
[logging]
    default = FILE:/var/log/krb5libs.log
    kdc = FILE:/var/log/krb5kdc.log
    admin_server = FILE:/var/log/kadmind.log
[domain_realm]
    .localhost = $KERBEROS_REALM
    localhost = $KERBEROS_REALM
EOF

sudo mkdir /etc/krb5kdc
sudo printf '*/*@%s\t*' "$KERBEROS_REALM" | sudo tee /etc/krb5kdc/kadm5.acl

sudo apt-get update
sudo apt-get install -y krb5-kdc krb5-admin-server

printf "$KERBEROS_PASSWORD\n$KERBEROS_PASSWORD" | sudo kdb5_util -r "$KERBEROS_REALM" create -s -W
for p in client server tom jerry; do
  sudo kadmin.local -q "addprinc -randkey $p/localhost@$KERBEROS_REALM"
  sudo kadmin.local -q "xst -k /tmp/$p.keytab $p/localhost@$KERBEROS_REALM"
  sudo chmod +rx /tmp/$p.keytab
done

echo "Restarting krb services..."
sudo service krb5-kdc restart
sudo service krb5-admin-server restart
# AWS: Lightsail first, EC2 for an existing VPC

Shape: [VM](../shapes/vm.md). Tool: `aws` (AWS CLI v2).

Verified how: not run end to end. Written from AWS's documentation and CLI reference and checked against loadout's deploy files, on 2026-10-07. The Lightsail commands match the CLI reference page for each command. The EC2 image lookup uses a filter name from memory and has not been checked against Canonical's current naming. Nobody has created an instance with these exact commands, so if a flag or output differs from what `aws ... help` shows, trust the help and the documentation (below) and say what you changed.

Sources:
- https://docs.aws.amazon.com/cli/latest/reference/lightsail/ (create-instances, import-key-pair, put-instance-public-ports, allocate-static-ip, attach-static-ip, create-instance-snapshot)
- https://aws.amazon.com/lightsail/pricing/ (bundles and snapshot price)
- https://docs.aws.amazon.com/lightsail/latest/userguide/understanding-ssh-in-amazon-lightsail.html (default user names)
- https://repost.aws/knowledge-center/ec2-port-25-throttle (port 25 is blocked outgoing)
- https://docs.aws.amazon.com/cli/latest/reference/ec2/import-key-pair.html (RSA and ED25519 public keys)

## Before you start

- An AWS account and `aws` v2 with working credentials for it. The person sets that up themselves (`aws configure`, or `aws configure sso` then `aws sso login`) and you check with `aws sts get-caller-identity`. Read nothing from `~/.aws`. Ask which profile and region to use, and pass `--region REGION` and `--profile NAME` on every command (or have the person export `AWS_PROFILE` and `AWS_REGION` in their own shell).
- The person's public key (a `.pub` file). Lightsail's `import-key-pair` accepts only an `ssh-rsa` key (its CLI reference says so, checked 2026-10-07), while EC2 takes RSA or ED25519. For Lightsail the person needs an RSA key: `ssh-keygen -t rsa -b 4096 -f ~/.ssh/loadout_rsa` in their own terminal (they choose the passphrase), then `ssh-add ~/.ssh/loadout_rsa` so `ssh` and `deploy.sh` find it. You only ever read `loadout_rsa.pub`.
- AWS blocks outgoing port 25 for EC2, and throttles it on Lightsail. Port 587 is not affected: SES and any other provider on 587 with STARTTLS work (checked 2026-10-07).
- Lightsail is a flat monthly bundle in one region. EC2 is billed per resource. Prefer Lightsail unless the person already has a VPC to put this in.

## Create the machine

### Lightsail

Look up what exists, then show the person the bundle and its price:

```sh
aws lightsail get-blueprints --query 'blueprints[?contains(blueprintId, `ubuntu`)].[blueprintId,name]' --output table
aws lightsail get-bundles --query 'bundles[?supportedPlatforms[0]==`LINUX_UNIX` && !contains(bundleId, `ipv6`)].[bundleId,price,cpuCount,ramSizeInGb,diskSizeInGb]' --output table
aws lightsail get-regions --include-availability-zones --query 'regions[].[name,availabilityZones[].zoneName]'
```

Choose an Ubuntu blueprint (`ubuntu_24_04` at the time of writing) and the bundle with 1 GB of memory and a public IPv4 address (not the IPv6-only bundles). Lightsail runs a launch script, not cloud-init, as user data, and its Ubuntu image already has a user `ubuntu` with the key you import and passwordless sudo, so skip user data and deploy with `LOADOUT_SSH_USER=ubuntu`.

```sh
aws lightsail import-key-pair --key-pair-name loadout-admin --public-key-base64 "$(base64 < ~/.ssh/loadout_rsa.pub | tr -d '\n')"
aws lightsail create-instances --instance-names loadout --availability-zone ZONE --blueprint-id ubuntu_24_04 --bundle-id BUNDLE_ID --key-pair-name loadout-admin
aws lightsail allocate-static-ip --static-ip-name loadout-ip
aws lightsail attach-static-ip --static-ip-name loadout-ip --instance-name loadout
aws lightsail get-static-ip --static-ip-name loadout-ip --query 'staticIp.ipAddress' --output text
```

`BUNDLE_ID` and `ZONE` come from the lists above. A static IP stays when the instance is replaced and is free while attached. One left unattached is billed, so it is in the tear down list. Wait until `aws lightsail get-instance --instance-name loadout --query 'instance.state.name' --output text` says `running`. Record the instance, key pair and static IP names.

### EC2 (an existing VPC)

Use this only when the person names the VPC and subnet. Ask for both, and for a security group policy if they have one.

```sh
aws ec2 import-key-pair --key-name loadout-admin --public-key-material fileb://$HOME/.ssh/id_ed25519.pub
aws ec2 create-security-group --group-name loadout-registry --description "loadout registry" --vpc-id VPC_ID
aws ec2 describe-images --owners 099720109477 --filters 'Name=name,Values=ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*' --query 'sort_by(Images,&CreationDate)[-1].ImageId' --output text
aws ec2 run-instances --image-id AMI_ID --instance-type t3.micro --key-name loadout-admin --security-group-ids SG_ID --subnet-id SUBNET_ID --user-data file://cloud-init.yaml --block-device-mappings 'DeviceName=/dev/sda1,Ebs={VolumeSize=20,VolumeType=gp3}' --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=loadout}]'
aws ec2 allocate-address --domain vpc
aws ec2 associate-address --instance-id INSTANCE_ID --allocation-id ALLOCATION_ID
```

`099720109477` is Canonical's account. For an Arm machine use a `t4g` type, the `arm64` image name and `LOADOUT_ARCH=arm64`. A `t3.micro` has 1 GB of memory. The person may prefer `t3.small` (2 GB). The cloud-init file is the [VM shape](../shapes/vm.md)'s, with the key replaced. It creates `admin`. Without user data the default user is `ubuntu`.

## Firewall

Lightsail: new Linux instances start with a short default list (SSH and HTTP), and `put-instance-public-ports` closes every port it does not name, so write all four rules at once. Each instance has an IPv4 and an IPv6 firewall, so read the port states afterwards and, if the person uses IPv6, check the rules cover it (the CLI reference for the `cidrs` and `ipv6Cidrs` fields says what applies when they are left out):

```sh
aws lightsail put-instance-public-ports --instance-name loadout --port-infos fromPort=22,toPort=22,protocol=tcp fromPort=80,toPort=80,protocol=tcp fromPort=443,toPort=443,protocol=tcp fromPort=443,toPort=443,protocol=udp
aws lightsail get-instance-port-states --instance-name loadout
```

EC2: allow the same four from anywhere on the security group:

```sh
aws ec2 authorize-security-group-ingress --group-id SG_ID --ip-permissions 'IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=0.0.0.0/0}]' 'IpProtocol=tcp,FromPort=80,ToPort=80,IpRanges=[{CidrIp=0.0.0.0/0}]' 'IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=0.0.0.0/0}]' 'IpProtocol=udp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=0.0.0.0/0}]'
```

`prepare.sh` also sets `ufw` inside the machine.

## Name

[dns.md](../dns.md): an A record to the static IP (Lightsail) or the Elastic IP (EC2), or `<ip-with-dashes>.sslip.io`. The person may keep DNS in Route 53. Ask before changing it.

## Deploy

The [VM shape](../shapes/vm.md), step 3, with `LOADOUT_HOST` the static or elastic IP and `LOADOUT_SSH_USER=ubuntu` on Lightsail (or `admin` on EC2 with the cloud-init file). `prepare.sh` then allows only that user to ssh in.

## Off-host backups

- Lightsail: a snapshot before upgrades, `aws lightsail create-instance-snapshot --instance-name loadout --instance-snapshot-name loadout-before-upgrade`. Snapshots are billed by size per month (see Cost).
- EC2: an EBS snapshot of the root volume (`aws ec2 create-snapshot --volume-id VOLUME_ID`), or Data Lifecycle Manager for a schedule.
- Always also pull the nightly archive to the person's computer, see [backups.md](../backups.md).

## Cost

Lightsail bundles with a public IPv4 address, from the pricing page checked 2026-10-07: 5 USD (512 MB), 7 USD (1 GB, 2 vCPU, 40 GB disk, 2 TB transfer), 12 USD (2 GB). One 1 GB bundle is enough, so about 7 USD per month. Snapshots cost 0.05 USD per GB per month. The static IP is included while attached. Data transfer beyond the bundle's allowance is billed. A registry does not come near it. EC2 is billed by the hour for the instance, its volume and an Elastic IP. `t3.micro` in most regions is under 10 USD per month plus the volume, but read the current price (https://aws.amazon.com/ec2/pricing/on-demand/) and tell the person.

## Tear down

Only after a yes for exactly this, and only what the notes file lists.

```sh
aws lightsail delete-instance --instance-name loadout
aws lightsail release-static-ip --static-ip-name loadout-ip
aws lightsail delete-key-pair --key-pair-name loadout-admin
aws lightsail get-instance-snapshots --query 'instanceSnapshots[].name'
```

Delete a snapshot you made with `aws lightsail delete-instance-snapshot --instance-snapshot-name NAME` after the person agrees. For EC2: `aws ec2 terminate-instances --instance-ids INSTANCE_ID`, then `aws ec2 release-address --allocation-id ALLOCATION_ID`, the security group and the key pair (`aws ec2 delete-security-group --group-id SG_ID`, `aws ec2 delete-key-pair --key-name loadout-admin`), and any snapshots. Remove the DNS records the person added.

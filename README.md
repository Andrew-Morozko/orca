# Orca
_Lazy container orchestration_

**WARNING**: It works, but has no documentation or tests. Use it at your own risk.

Orca creates new Docker containers on demand and saves your resources. When user request arrives (HTTP and SSH are supported at the moment) Orca:

* Determines user identity (via SSH login or HTTP cookie)
* Determines desired image (via interactive menu for SSH or subdomain for HTTP)
* Checks for existing user connections and the user already has an active connection – uses it.
* Otherwise, Orca attempts to find a running container with the desired image and free user slots, and if successful – assigns the user to that container (useful for multi-user HTTP servers, not so much for SSH).
* If all fails – Orca launches a new container and assigns a user to it.

After the user has been assigned to container all traffic is proxied back and forth.

User sessions could be configured to time out after a certain period of inactivity.
Containers can be configured to:
Kick users out after a period of inactivity
Stop accepting new users after a max number of concurrent users was reached
Stop accepting new users and shutdown after all current users have left when the maximum total number of users served or maximum lifetime was reached

Orca is configured by placing labels on Docker Images:
* `orca.kind` – image kind. "web" or "ssh" available, "tcp" planned
* `orca.name` – image name. By default - name(repo tag) of the image
* `orca.port` – 80 for web images. Port of HTTP server inside the container

* `orca.timeout.session` – "24h". Maximum container lifespan
* `orca.timeout.inactive` – "15m". Maximum user inactivity period 

* `orca.users.total` – 1 for SSH images, -1 for web images. Maximum number of users served over container lifetime
* `orca.users.concurrent` – 1 for SSH images, -1 for web images. Maximum number of simultaneous users

* `orca.connection.method` – "attach" for SSH images. Attach executes "docker attach". Planned methods: "connect" (to tcp port), "exec" (perform "docker exec")

* `orca.container.stopsignal` – signal to stop the container
* `orca.container.persistBetweenReconnects` – true for web connections, false for other connections. Determines if connection termination means that user has left the container

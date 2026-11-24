# Project rules

## Deploying

After every change to the code, deploy it to the Docker host without being asked:

1. `docker build -q -t tesla-homepage:dev .`
2. `docker rm -f tesla-homepage`
3. `docker run -d --name tesla-homepage -p 8090:8080 -v tesla-homepage-data:/data tesla-homepage:dev`
4. Check `docker logs tesla-homepage` shows `listening`.

Keep the `tesla-homepage-data` volume. Never remove it.

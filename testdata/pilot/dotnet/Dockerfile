FROM mcr.microsoft.com/dotnet/sdk:10.0 AS build

WORKDIR /src
COPY Draincheck.Pilot.csproj Program.cs ./
RUN dotnet publish Draincheck.Pilot.csproj \
    --configuration Release \
    --output /out \
    --property:UseAppHost=false

FROM mcr.microsoft.com/dotnet/aspnet:10.0
WORKDIR /app
COPY --from=build /out/ ./

ENV ASPNETCORE_URLS=http://0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["dotnet", "Draincheck.Pilot.dll"]

FROM mcr.microsoft.com/dotnet/sdk:9.0 AS build
WORKDIR /app

COPY SampleApi.csproj ./
RUN dotnet restore

COPY . ./
RUN dotnet publish -c Release -o out

FROM mcr.microsoft.com/dotnet/aspnet:9.0
WORKDIR /app

COPY --from=build /app/out .

COPY --from=langango-build /app/langango.dll .
COPY --from=langango-build /app/liblangango_bridge.so .

ENV DOTNET_STARTUP_HOOKS=/app/langango.dll
EXPOSE 80

ENTRYPOINT ["dotnet", "SampleApi.dll"]
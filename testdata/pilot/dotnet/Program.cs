var builder = WebApplication.CreateSlimBuilder(args);
builder.Services.Configure<HostOptions>(options =>
{
    options.ShutdownTimeout = TimeSpan.FromSeconds(10);
});
builder.Services.AddSingleton<LifecycleState>();

var app = builder.Build();
var lifecycle = app.Services.GetRequiredService<LifecycleState>();
app.Lifetime.ApplicationStopping.Register(() => lifecycle.Ready = false);

app.MapGet("/ready", (LifecycleState state) =>
    state.Ready ? Results.Ok() : Results.StatusCode(StatusCodes.Status503ServiceUnavailable));

app.MapGet("/work", async (HttpContext context) =>
{
    var delay = 2000;
    if (context.Request.Query.TryGetValue("delay_ms", out var value) &&
        int.TryParse(value, out var requestedDelay))
    {
        delay = Math.Clamp(requestedDelay, 0, 30000);
    }
    await Task.Delay(delay);
    return Results.Text("completed\n");
});

app.Run();

sealed class LifecycleState
{
    public volatile bool Ready = true;
}

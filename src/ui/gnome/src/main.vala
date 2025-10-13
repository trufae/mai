using Gtk;
using Adw;

public class MaiApplication : Gtk.Application {
    private MCPClient mcp_client;

    public MaiApplication () {
        Object (application_id: "com.mai.gnome", flags: ApplicationFlags.FLAGS_NONE);
        mcp_client = new MCPClient ();
    }

    protected override void startup () {
        base.startup ();

        // Initialize MCP client
        mcp_client.initialize.begin ((obj, res) => {
            if (!mcp_client.initialize.end (res)) {
                // Handle initialization error
            }
        });

        var new_window_action = new SimpleAction ("new-window", null);
        new_window_action.activate.connect (() => {
            var window = new ChatWindow (this, mcp_client);
            window.present ();
        });
        add_action (new_window_action);

        var quit_action = new SimpleAction ("quit", null);
        quit_action.activate.connect (() => {
            this.quit ();
        });
        add_action (quit_action);

        set_accels_for_action ("app.new-window", {"<Control>n"});
        set_accels_for_action ("app.quit", {"<Control>q"});
        set_accels_for_action ("win.close", {"<Control>w"});
    }

    protected override void activate () {
        var window = new ChatWindow (this, mcp_client);
        window.present ();
    }

    public static int main (string[] args) {
        var app = new MaiApplication ();
        return app.run (args);
    }
}

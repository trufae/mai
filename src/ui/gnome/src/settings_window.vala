using Gtk;
using Adw;

public class SettingsWindow : Gtk.Box {
    private Gtk.DropDown provider_combo;
    private Gtk.DropDown model_combo;
    private MCPClient mcp_client;

    public SettingsWindow (MCPClient client) {
        Object (orientation: Gtk.Orientation.VERTICAL, spacing: 12);
        mcp_client = client;
        setup_ui ();
    }

    private void setup_ui () {
        var provider_label = new Gtk.Label ("Provider:");
        var provider_model = new Gtk.StringList (null);
        provider_model.append ("OpenAI");
        provider_model.append ("Anthropic");
        provider_model.append ("Local");
        provider_combo = new Gtk.DropDown (provider_model, null);

        var model_label = new Gtk.Label ("Model:");
        var model_model = new Gtk.StringList (null);
        model_combo = new Gtk.DropDown (model_model, null);

        append (provider_label);
        append (provider_combo);
        append (model_label);
        append (model_combo);

        provider_combo.notify["selected"].connect (on_provider_changed);
    }

    private void on_provider_changed () {
        var selected = provider_combo.selected;
        if (selected >= 0) {
            var provider = (provider_combo.model as Gtk.StringList).get_string (selected);
            var args = new HashTable<string, Value?> (str_hash, str_equal);
            args["provider"] = provider.down ();
            mcp_client.call_tool.begin ("set_provider", args, (obj, res) => {
                // Handle response
            });
            load_models ();
        }
    }

    private void load_models () {
        mcp_client.call_tool.begin ("list_models", new HashTable<string, Value?> (str_hash, str_equal), (obj, res) => {
            // Parse and populate model_combo
        });
    }
}
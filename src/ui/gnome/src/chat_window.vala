using Gtk;
using Adw;

public class ChatWindow : Gtk.ApplicationWindow {
    private Adw.NavigationSplitView split_view;
    private Gtk.ListBox sidebar;
    private Gtk.Stack content_stack;
    private Gtk.ScrolledWindow messages_scroll;
    private Gtk.ListBox messages_list;
    private Gtk.Entry input_entry;
    private Gtk.Button send_button;
    private SettingsWindow settings_window;
    private MCPClient mcp_client;
    private bool is_waiting = false;

    public ChatWindow (Gtk.Application app) {
        Object (application: app, title: "Mai", default_width: 800, default_height: 600);

        mcp_client = new MCPClient ();
        setup_ui ();
        setup_mcp ();
    }

    private void setup_ui () {
        // Add close action
        var close_action = new SimpleAction ("close", null);
        close_action.activate.connect (() => {
            this.close ();
        });
        add_action (close_action);

        // Add CSS for user messages
        var css_provider = new Gtk.CssProvider ();
        css_provider.load_from_data (".user-message { background-color: #f0f0f0; }".data);
        Gtk.StyleContext.add_provider_for_display (get_display (), css_provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION);

        split_view = new Adw.NavigationSplitView ();
        split_view.min_sidebar_width = 0;

        var header = new Adw.HeaderBar ();
        var toggle_button = new Gtk.ToggleButton ();
        toggle_button.icon_name = "sidebar-show-symbolic";
        toggle_button.tooltip_text = "Toggle sidebar";
        toggle_button.toggled.connect (() => {
            split_view.collapsed = toggle_button.active;
            if (toggle_button.active) {
                split_view.show_content = true;
            }
        });
        header.pack_start (toggle_button);
        set_titlebar (header);

        set_child (split_view);

        // Sidebar
        var sidebar_page = new Adw.NavigationPage (new Gtk.Label ("Sidebar"), "Sidebar");
        var sidebar_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
        sidebar = new Gtk.ListBox ();
        var chat_row = new Gtk.ListBoxRow ();
        var chat_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        chat_row_box.margin_start = 8;
        var chat_icon = new Gtk.Image.from_icon_name ("face-smile-symbolic");
        chat_icon.pixel_size = 24;
        chat_row_box.append (chat_icon);
        chat_row_box.append (new Gtk.Label ("Chat"));
        chat_row.child = chat_row_box;
        chat_row.set_size_request (-1, 48); // Minimum height
        sidebar.append (chat_row);
        var settings_row = new Gtk.ListBoxRow ();
        var settings_row_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        settings_row_box.margin_start = 8;
        var settings_icon = new Gtk.Image.from_icon_name ("preferences-system");
        settings_icon.pixel_size = 24;
        settings_row_box.append (settings_icon);
        settings_row_box.append (new Gtk.Label ("Settings"));
        settings_row.child = settings_row_box;
        settings_row.set_size_request (-1, 48); // Minimum height
        sidebar.append (settings_row);
        sidebar.row_selected.connect (on_sidebar_selected);
        sidebar_box.append (sidebar);
        sidebar_page.child = sidebar_box;
        split_view.sidebar = sidebar_page;

        // Content Stack
        var content_page = new Adw.NavigationPage (new Gtk.Label ("Content"), "Content");
        content_stack = new Gtk.Stack ();
        content_page.child = content_stack;
        split_view.content = content_page;

        // Chat View
        var chat_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);

        // Messages
        messages_scroll = new Gtk.ScrolledWindow ();
        messages_scroll.vexpand = true;
        messages_list = new Gtk.ListBox ();
        messages_scroll.child = messages_list;
        chat_box.append (messages_scroll);

        // Input
        var input_box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        input_box.margin_start = 16;
        input_box.margin_end = 16;
        input_box.margin_top = 16;
        input_box.margin_bottom = 16;
        input_entry = new Gtk.Entry ();
        input_entry.hexpand = true;
        input_entry.placeholder_text = "Type your message...";
        input_entry.activate.connect (send_message);
        input_box.append (input_entry);

        send_button = new Gtk.Button.with_label ("Send");
        send_button.clicked.connect (send_message);
        input_box.append (send_button);

        chat_box.append (input_box);

        content_stack.add_named (chat_box, "chat");

        // Settings View
        settings_window = new SettingsWindow (mcp_client);
        content_stack.add_named (settings_window, "settings");

        content_stack.visible_child_name = "chat";
    }

    private void on_sidebar_selected (Gtk.ListBoxRow? row) {
        if (row == null) return;
        var index = sidebar.get_selected_row ().get_index ();
        if (index == 0) {
            content_stack.visible_child_name = "chat";
        } else if (index == 1) {
            content_stack.visible_child_name = "settings";
        }
    }

    private void setup_mcp () {
        mcp_client.initialize.begin ((obj, res) => {
            if (mcp_client.initialize.end (res)) {
                // Connected
            } else {
                // Error
            }
        });
    }

    private void send_message () {
        var text = input_entry.text.strip ();
        if (text == "" || is_waiting) return;

        add_message (text, false);
        input_entry.text = "";
        is_waiting = true;

        var args = new HashTable<string, Value?> (str_hash, str_equal);
        args["message"] = text;
        args["stream"] = "false";

        mcp_client.call_tool.begin ("send_message", args, (obj, res) => {
            try {
                var response = mcp_client.call_tool.end (res);
                add_message (response, true);
            } catch (Error e) {
                add_message ("Error: " + e.message, true);
            }
            is_waiting = false;
        });
    }

    private void add_message (string text, bool is_ai) {
        var row = new Gtk.ListBoxRow ();
        if (!is_ai) {
            row.add_css_class ("user-message");
            row.halign = Gtk.Align.END;
        } else {
            row.halign = Gtk.Align.START;
        }
        var box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        var label = new Gtk.Label (text);
        label.wrap = true;
        label.xalign = 0;
        box.append (label);
        row.child = box;
        messages_list.append (row);
    }
}

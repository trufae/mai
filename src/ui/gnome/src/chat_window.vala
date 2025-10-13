using Gtk;
using Adw;

public class ChatWindow : Adw.ApplicationWindow {
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

    public ChatWindow (Adw.Application app) {
        Object (application: app, title: "MAI Chat", default_width: 800, default_height: 600);

        mcp_client = new MCPClient ();
        setup_ui ();
        setup_mcp ();
    }

    private void setup_ui () {
        split_view = new Adw.NavigationSplitView ();
        set_content (split_view);

        // Sidebar
        var sidebar_page = new Adw.NavigationPage (new Gtk.Label ("Sidebar"), "Sidebar");
        var sidebar_box = new Gtk.Box (Gtk.Orientation.VERTICAL, 0);
        sidebar = new Gtk.ListBox ();
        var chat_row = new Gtk.ListBoxRow ();
        chat_row.child = new Gtk.Label ("Chat");
        sidebar.append (chat_row);
        var settings_row = new Gtk.ListBoxRow ();
        settings_row.child = new Gtk.Label ("Settings");
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
        input_entry = new Gtk.Entry ();
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
        var box = new Gtk.Box (Gtk.Orientation.HORIZONTAL, 6);
        var label = new Gtk.Label (text);
        label.wrap = true;
        label.xalign = is_ai ? 0 : 1;
        box.append (label);
        messages_list.append (box);
    }
}
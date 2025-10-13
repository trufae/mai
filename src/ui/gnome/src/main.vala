using Gtk;
using Adw;

public class MaiApplication : Gtk.Application {
    public MaiApplication () {
        Object (application_id: "com.mai.gnome", flags: ApplicationFlags.FLAGS_NONE);
    }

    protected override void activate () {
        var window = new ChatWindow (this);
        window.present ();
    }

    public static int main (string[] args) {
        var app = new MaiApplication ();
        return app.run (args);
    }
}
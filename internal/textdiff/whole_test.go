package textdiff

import "testing"

func TestWholeAndMarked(t *testing.T) {
	l := Labels{Base: "installed 1.2.0", Ours: "yours (edited 1.2.0)", Theirs: "release 1.3.0"}
	got := Whole(nil, []byte("mine"), []byte("theirs\n"), l)
	want := "<<<<<<< yours (edited 1.2.0)\nmine\n||||||| installed 1.2.0\n=======\ntheirs\n>>>>>>> release 1.3.0\n"
	if string(got) != want {
		t.Errorf("Whole() =\n%s\nwant\n%s", got, want)
	}
	if !Marked(got, l) {
		t.Error("Marked(Whole()) = false")
	}
	if Marked(got, Labels{Base: "base 2.0.0", Ours: "yours", Theirs: "release 3.0.0"}) {
		t.Error("Marked() matched another merge's labels")
	}
	if Marked([]byte("mine\ntheirs\n"), l) {
		t.Error("Marked() of resolved text = true")
	}
	if !Marked([]byte("a\r\n>>>>>>> release 1.3.0\r\n"), l) {
		t.Error("Marked() missed a marker with CRLF")
	}
}

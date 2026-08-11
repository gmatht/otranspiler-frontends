// Cov.java — ANTLR4 parser-coverage harness for the coverage test.
// Parses each file with the generated parser, counts lexer+parser
// syntax errors (default error recovery — the tolerant measure), and
// prints one line per file + a coverage summary.
//
// Usage: java -cp <out>:<antlr4.jar> Cov <mode> <file>...
//   mode = posix  → POSIXLexer/POSIXParser, start rule `start`
//   mode = py     → Python3Lexer/Python3Parser, start rule `file_input`
import org.antlr.v4.runtime.*;
import java.nio.file.*;

public class Cov {
    public static void main(String[] args) throws Exception {
        if (args.length < 2) {
            System.err.println("usage: Cov <posix|py> <file>...");
            System.exit(2);
        }
        String mode = args[0];
        int total = 0, ok = 0;
        for (int i = 1; i < args.length; i++) {
            String p = args[i];
            String src;
            try {
                src = Files.readString(Paths.get(p));
            } catch (Exception e) {
                System.err.println("READ-ERR " + p + " (" + e.getMessage() + ")");
                continue;
            }
            total++;
            int errs;
            try {
                final int[] nerr = {0};
                org.antlr.v4.runtime.BaseErrorListener el =
                    new org.antlr.v4.runtime.BaseErrorListener() {
                        public void syntaxError(org.antlr.v4.runtime.Recognizer<?, ?> r,
                                Object o, int line, int col, String msg,
                                org.antlr.v4.runtime.RecognitionException e) {
                            nerr[0]++;
                        }
                    };
                if (mode.equals("posix")) {
                    POSIXLexer lx = new POSIXLexer(CharStreams.fromString(src));
                    lx.removeErrorListeners(); lx.addErrorListener(el);
                    CommonTokenStream toks = new CommonTokenStream(lx);
                    POSIXParser ps = new POSIXParser(toks);
                    ps.removeErrorListeners(); ps.addErrorListener(el);
                    ps.start();
                } else {
                    Python3Lexer lx = new Python3Lexer(CharStreams.fromString(src));
                    lx.removeErrorListeners(); lx.addErrorListener(el);
                    CommonTokenStream toks = new CommonTokenStream(lx);
                    Python3Parser ps = new Python3Parser(toks);
                    ps.removeErrorListeners(); ps.addErrorListener(el);
                    ps.file_input();
                }
                errs = nerr[0];
            } catch (Throwable e) {
                errs = 999; // lexer/parser exception (e.g. runaway recursion) = fail
            }
            if (errs == 0) ok++;
            System.out.println((errs == 0 ? "OK  " : "ERR ") + p
                + (errs > 0 && errs < 999 ? " (" + errs + " errors)" : ""));
        }
        System.out.printf("COVERAGE %s: %d/%d = %.1f%%%n", mode, ok, total,
            100.0 * ok / (total == 0 ? 1 : total));
    }
}

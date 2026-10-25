import SwiftUI

/// The landing page's paintings (`site/src/assets/backdrops`), one per step of signing in.
enum Painting: String {
    case roadside, meadow, bigCloud = "big-cloud", skyFields = "sky-fields", plains

    var image: Image { Image("painting-" + rawValue) }

    /// The sky's colour, behind the painting while it decodes and under the title for contrast.
    var sky: Color {
        switch self {
        case .roadside: Color(hex: 0x13348C)
        case .meadow: Color(hex: 0x2C5FA8)
        case .bigCloud: Color(hex: 0x3466B0)
        case .skyFields: Color(hex: 0x2F63B4)
        case .plains: Color(hex: 0x2251D0)
        }
    }

    /// Where across the painting the frame looks when it is narrower than the painting
    /// (0 the left edge, 1 the right): toward open sky, away from the brightest clouds.
    var focus: CGFloat {
        switch self {
        case .roadside: 0.3
        case .meadow: 0.3
        case .bigCloud: 0.04
        case .skyFields: 0.35
        case .plains: 0.3
        }
    }
}

/// A painting filling its frame, centred, calmer in dark mode, with the sky deepened
/// at the top so white type over it reads (the site's hero does the same).
struct PaintingView: View {
    let painting: Painting
    @Environment(\.colorScheme) private var scheme

    /// Every painting is 2560 by 1654.
    private static let size = CGSize(width: 2560, height: 1654)
    /// The site hero's overlay blue.
    private static let ink = Color(red: 16 / 255, green: 46 / 255, blue: 116 / 255)

    var body: some View {
        Rectangle()
            .fill(painting.sky)
            .overlay {
                GeometryReader { geo in
                    let scale = max(geo.size.width / Self.size.width, geo.size.height / Self.size.height)
                    let width = Self.size.width * scale
                    painting.image
                        .resizable()
                        .frame(width: width, height: Self.size.height * scale)
                        .offset(x: -(width - geo.size.width) * painting.focus)
                }
            }
            .overlay {
                LinearGradient(
                    stops: [
                        .init(color: Self.ink.opacity(0.72), location: 0),
                        .init(color: Self.ink.opacity(0.42), location: 0.32),
                        .init(color: Self.ink.opacity(0.1), location: 0.55),
                        .init(color: .clear, location: 0.72),
                    ],
                    startPoint: .top,
                    endPoint: .bottom
                )
            }
            .overlay { if scheme == .dark { Color.black.opacity(0.26) } }
            .clipped()
            .accessibilityHidden(true)
    }
}

/// Every auth screen's frame: a painting across the top, the hero's type over its sky,
/// and the actions on a panel anchored at the bottom, in reach of a thumb. The painting
/// always ends just under the panel, so a taller panel (a form opening) draws it smaller.
/// `decoration` fills the space between the two when it fits, and hides when it doesn't.
struct AuthScene<Hero: View, Decoration: View, Panel: View>: View {
    let painting: Painting
    @ViewBuilder var hero: () -> Hero
    @ViewBuilder var decoration: () -> Decoration
    @ViewBuilder var panel: () -> Panel

    @State private var panelHeight: CGFloat = 0

    var body: some View {
        GeometryReader { geo in
            ScrollView {
                VStack(spacing: 0) {
                    hero()
                        .frame(maxWidth: 480, alignment: .leading)
                        .frame(maxWidth: .infinity)
                        .padding(.horizontal, 24)
                        .padding(.top, 10)
                        .padding(.bottom, 24)
                    Spacer(minLength: 0)
                        .overlay {
                            ViewThatFits(in: .vertical) {
                                decoration().padding(.vertical, 20)
                                Color.clear.frame(height: 0)
                            }
                        }
                    AuthPanel { panel() }
                        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { panelHeight = $0 }
                }
                .frame(minHeight: geo.size.height)
            }
            .scrollBounceBehavior(.basedOnSize)
            .scrollDismissesKeyboard(.interactively)
            .scrollEdgeEffectHidden(true, for: .top)
            .background(alignment: .top) {
                PaintingView(painting: painting)
                    .frame(height: max(240, geo.safeAreaInsets.top + geo.size.height - panelHeight + 40))
                    .ignoresSafeArea(edges: .top)
                    .motion(Motion.smooth, value: panelHeight)
            }
        }
        .background(Theme.surface.ignoresSafeArea())
    }
}

extension AuthScene where Decoration == EmptyView {
    init(painting: Painting, @ViewBuilder hero: @escaping () -> Hero, @ViewBuilder panel: @escaping () -> Panel) {
        self.init(painting: painting, hero: hero, decoration: { EmptyView() }, panel: panel)
    }
}

/// The white sheet the actions sit on, its top corners rounded over the painting.
struct AuthPanel<Content: View>: View {
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            content()
        }
        .frame(maxWidth: 480)
        .frame(maxWidth: .infinity)
        .padding(.horizontal, 24)
        .padding(.top, 26)
        .padding(.bottom, 12)
        .background(alignment: .top) {
            UnevenRoundedRectangle(topLeadingRadius: 30, topTrailingRadius: 30, style: .continuous)
                .fill(Theme.surface)
                .padding(.bottom, -600)
                .shadow(color: .black.opacity(0.14), radius: 24, y: -2)
        }
    }
}

/// White type over a painting's sky.
struct HeroTitle: View {
    let title: String
    var subtitle: String?
    var size: CGFloat = 30

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(ODFont.sans(size, weight: .semibold))
                .tracking(-size * 0.028)
                .foregroundStyle(.white)
                .fixedSize(horizontal: false, vertical: true)
            if let subtitle {
                Text(subtitle)
                    .font(ODFont.sans(15.5))
                    .foregroundStyle(.white.opacity(0.86))
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .shadow(color: Color(hex: 0x0A1F55).opacity(0.35), radius: 14, y: 2)
    }
}

/// The page frame every pushed auth step shares: its painting and title above, the form below.
struct AuthPage<Content: View>: View {
    var title: String
    var subtitle: String?
    var painting: Painting = .skyFields
    @ViewBuilder var content: () -> Content

    var body: some View {
        AuthScene(painting: painting) {
            HeroTitle(title: title, subtitle: subtitle)
                .padding(.top, 6)
        } panel: {
            content()
        }
        .toolbarBackground(.hidden, for: .navigationBar)
    }
}

/// The app at work, floating over the sign-in painting the way the site's hero shows it:
/// a customer's ticket and the reply Ollie drafted for it.
struct HeroCards: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var shown = false

    var body: some View {
        ZStack(alignment: .topLeading) {
            ticket
                .floating(reduceMotion: reduceMotion, delay: 0)
            ollie
                .padding(.leading, 64)
                .padding(.top, 96)
                .floating(reduceMotion: reduceMotion, delay: 1.4)
        }
        .frame(maxWidth: 360, alignment: .leading)
        .frame(maxWidth: .infinity)
        .opacity(shown ? 1 : 0)
        .offset(y: shown || reduceMotion ? 0 : 14)
        .onAppear {
            withAnimation(reduceMotion ? nil : Motion.smooth.delay(0.15)) { shown = true }
        }
        .accessibilityHidden(true)
    }

    private var ticket: some View {
        HStack(alignment: .top, spacing: 10) {
            Avatar(name: "Dana Whitfield", seed: "dana", size: 32)
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text("Dana Whitfield").font(ODFont.sans(13.5, weight: .semibold)).foregroundStyle(Theme.fg)
                    Spacer(minLength: 8)
                    Text("2m").font(ODFont.mono(11)).foregroundStyle(Theme.fg3)
                }
                Text("Charged twice for the March invoice")
                    .font(ODFont.sans(13))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1)
                HStack(spacing: 6) {
                    StatusPill(status: .open)
                    TonePill(text: "Billing", tone: .blue)
                }
                .padding(.top, 2)
            }
        }
        .padding(12)
        .frame(width: 286)
        .glassEffect(.regular.tint(Theme.glassTint), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private var ollie: some View {
        HStack(spacing: 10) {
            OllieMark(size: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text("Ollie drafted a reply").font(ODFont.sans(13, weight: .semibold)).foregroundStyle(Theme.fg)
                Text("Your refund is on its way, Dana.")
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .frame(width: 262, alignment: .leading)
        .glassEffect(.regular.tint(Theme.glassTint), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
    }
}

private extension View {
    /// A slow drift up and down; still with Reduce Motion.
    func floating(reduceMotion: Bool, delay: Double) -> some View {
        modifier(Floating(reduceMotion: reduceMotion, delay: delay))
    }
}

private struct Floating: ViewModifier {
    let reduceMotion: Bool
    let delay: Double
    @State private var up = false

    func body(content: Content) -> some View {
        content
            .offset(y: up ? -4 : 4)
            .onAppear {
                guard !reduceMotion else { return }
                withAnimation(.easeInOut(duration: 3.2).repeatForever(autoreverses: true).delay(delay)) { up = true }
            }
    }
}
